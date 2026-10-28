# Contract — PAC-50: SSO writes need an IdP-admin step-up, and the domain rule is exact

- **Spec**: `docs/specs/PAC-50.md` v2.2 (v2.1 challenge answered; §9 Decisions
  added at this stage)
- **Manifest**: `contracts/features/PAC-50.yaml`
- **Fragment**: `contracts/openapi/paths/auth.yaml` (plus its notes, section (d))
- **Absorbs**: PAC-46 (the exact-domain rule)
- **Revision**: 8, 2026-10-28 (1 and 2 on 2026-10-25, 3 and 4 on 2026-10-27, 5 to 7 earlier on 2026-10-28; see the Revision sections at the end). **Revision 8 merges `main` (PAC-22, PAC-73)**: `createSsoProvider` now carries PAC-22's request, 201 (`SsoProvider`) and 400 list inside PAC-50's gate, `setSsoProviderState` is the third gated write, `GET /api/admin/sso/stepup/options` is added for the Settings entry point, and the migration is `030_sso_admin_stepup`. Where sections 1-6 below describe `SsoProviderResponse`, the pre-PAC-22 request, two write operations or migration 029, "Revision 8" supersedes them. Revision 7 changes no operation or schema (the bundle is revision 6's): it replaces spec §9 D-13 with the owner's answer (a Settings entry point for the step-up, AC-25) and corrects stale lines in the manifest and in section 2. Revision 6 answers the round-4 challenge: `without_stepup` is an open list of codes and `stepup_scopes` carries a key only for a configured provider. Revision 5 records the owner decisions of 2026-10-27: sections 1 and 2 below describe revisions 1-4 where they say `provider_domain_mismatch` is a 400 or that the client navigates on a step-up 403; "Revision 5" supersedes both.

## 1. The change

*Sections 1 and 2 describe revisions 1-4. Where they differ from later revisions they are superseded (since revision 8, also for PAC-22's request and response, `setSsoProviderState` and the new options operation; see "Revision 8"), and each such place is marked: since revision 5 the 403 has four variants (`SsoWriteDomainMismatch`) and `provider_domain_mismatch` is a 403, not a 400; since revision 6 the step-up variant also carries `stepup_scopes` (a key per configured provider) and `without_stepup` (an open list). PAC-22 lands before PAC-50 (spec §9 D-12).*

Two operations change and two are added. Nothing else in the contract moves.

| Operation | Change |
|---|---|
| `createSsoProvider` `POST /api/admin/sso` | Documents the write gate of spec §4.2 in order. **400** gains `unknown property: <name>` and `provider_domain_mismatch` *(the latter a 403 since revision 5)*. **403** becomes `SsoWriteForbiddenResponse` (three variants, below; four since revision 5). The domain rule becomes exact and case-insensitive: `sub.example.com` from org `example.com` is refused. |
| `deleteSsoProvider` `DELETE /api/admin/sso/{domain}` | Same gate. The `{domain}` segment is lower-cased. **400** `provider_domain_mismatch` replaces the old 403 *(a 403 `provider_domain_mismatch` since revision 5; the delete has no 400 left)*. **403** becomes `SsoWriteForbiddenResponse`. **404** `sso provider not found` is new (API-067). **204** now means a row really was deleted. |
| `startSsoAdminStepup` `GET /api/admin/sso/stepup?provider=google\|microsoft` | **New.** Behind the session. Answers 302 to the IdP and sets the `sso_stepup_state` cookie. Errors: 400 (provider missing or unknown), 401, 403 (no org, D-1), 503 (provider not configured, D-2). |
| `ssoAdminStepupCallback` `GET /api/admin/sso/stepup/callback` | **New.** Behind the session. Answers 302 to `<FRONTEND_URL>/settings?stepup=granted` or `?stepup=denied&reason=<enum>`; the `Location` header carries a `pattern` that admits exactly those nine forms. Errors: 401 (no session), 500 (storage, D-4). |

Schemas:

- `SsoWriteForbiddenResponse` = `oneOf` three disjoint objects (four since revision 5, with `SsoWriteDomainMismatch`, `error` const `provider_domain_mismatch`):
  - `SsoWriteMembershipRequired`: `error` ∈ {`org membership required`, `org not found`}.
  - `SsoWriteStepupRequired`: `error` = `sso_write_requires_admin_stepup`, plus
    `stepup_url` = `/api/admin/sso/stepup` (required). Since revision 5 also
    `stepup_scopes` and `without_stepup` (required); since revision 6 the
    `stepup_scopes` keys are optional (present only for a configured provider)
    and `without_stepup` items are non-empty strings, not an enum.
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
belong to PAC-22, which lands after PAC-50 and rebases onto it (spec §4.9). *(Superseded by spec §9 D-12: PAC-22 lands first, and a later revision covers what it changes on these operations; see Revision 6, N2.)*

## 2. Why this shape

### The 403 is one status with three disjoint bodies

*Four disjoint bodies since revision 5 (`SsoWriteDomainMismatch`). The reasoning below is unchanged by that.*

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
- migration 030, `030_sso_admin_stepup` (027 before revision 3, 028 in revisions 3 and 4, 029 in revisions 5-7; spec §9 D-7), and the `lower(domain)` uniqueness (AC-20);
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
  every org (a 403 since revision 5). Is that acceptable, or does the plan need a router test?
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
   - `without_stepup: string[]`, an open enum (it was not: the schema was a
     closed enum; made a really open list in revision 6, B1), today only
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

## Challenge, revision 5 — 2026-10-28

Reviewed by `contract-challenger` (round 4) in a local session, against the
committed state of `factory/pac-50-contract` at `405b8d8`. Inputs: the
fragment `contracts/openapi/paths/auth.yaml`, the bundle, this manifest and
document, spec `docs/specs/PAC-50.md` and its §9. I did not read the author's
reasoning beyond these files.

**Verdict**: accept-with-changes (2 blocking, both in `SsoWriteStepupRequired`)

### Checks run (outputs as printed)

- `python scripts/openapi_assemble.py --check`: `OK: bundle is in sync (101 paths)`, rc=0.
- `python scripts/openapi_migration_report.py --check`: `OK: MIGRATION.md covers all 122 operations (4 generated)`, rc=0.
- `bash scripts/openapi_gen_go.sh --check`: `OK: backend/api/gen matches the contract`, rc=0.
- `go build ./...` in `backend/`: rc=0. The union has `As`/`From` accessors for all four variants (`paceday.gen.go:5106-5191`).
- `git show HEAD:contracts/openapi/openapi.yaml | sha256sum`: `8b9321ae…78ad`, the manifest's `contractHash`.
- Web `make openapi-check CONTRACT=<this bundle>` on smart-calendar-flow `chore/regen-types-pac-50` at `fdfb434` (#119), in a separate `node:24-bookworm` container (not the shared `paceday-gate` one): `OK: src/api/generated matches the contract`, rc=0.
- A zod probe (`npx tsx`) run against #119's generated `schemas.ts` at `fdfb434`. The rows below give each payload and its result.
- Not run: `api-gate.sh`, `web-gate.sh` and `e2e-gate.sh`. The host api checkout is on `impl/pac-22-sso-secrets-backend`, which another stage owns, and `web-gate.sh` clears the shared container's `/work`. The checks above cover this stage's gate items 1, 2 and 4. For item 3 I parsed the bundle: no `x-uncertain` on any of the four `/api/admin/sso*` paths or on any of the eight SSO schemas.

| Payload (403 body) | `SsoWriteStepupRequired.safeParse` | `SsoWriteForbiddenResponse.safeParse` |
|---|---|---|
| A: today's step-up body, `without_stepup: ["sso_configuration_writes"]`, both scope lists filled | true | true |
| B: A with `without_stepup: ["sso_configuration_writes","verified_domain_org_membership"]` | **false** | **false** |
| C: A with `stepup_scopes.microsoft: []` | **false** | **false** |
| D: A with no `microsoft` key | **false** | **false** |
| E: `{"error":"provider_domain_mismatch","stepup_url":"/api/admin/sso/stepup"}` | false | false |
| F: `{"error":"provider_domain_mismatch"}` | false | true |
| G: `{"error":"sso_write_requires_fresh_login","without_stepup":[…]}` | false | false |
| H: `{"error":"sso_write_requires_admin_stepup","stepup_url":"/api/admin/sso/stepup"}` (#212's body today) | false | false |

### Requests wrongly permitted

None found. See "Verified clean".

### Responses the client cannot handle / contract contradicts itself (blocking)

1. **B1: `without_stepup` is documented as open but generated as closed.**
   `SsoWriteStepupRequired.without_stepup` description: "Values may be
   added; a client ignores one it does not know". Revision 5 §2 calls it "an
   open enum", and spec §9 D-11 says PAC-68 "adds the value to the open
   `without_stepup` enum". The schema is `items: {type: string, enum:
   [sso_configuration_writes]}`, which is a closed enum. typed-openapi
   generates it as `z.array(z.literal("sso_configuration_writes"))` inside a
   `z.strictObject` (#119 `src/api/generated/schemas.ts:422`), and
   openapi-typescript as `"sso_configuration_writes"[]`. Payload B, the exact
   addition D-11 anticipates, fails both `SsoWriteStepupRequired` and
   `SsoWriteForbiddenResponse` (probe row B). So the day PAC-68 adds the
   value, a client that recognizes the 403 by schema (the way `client.ts`
   already recognizes `CalendarDisconnectUnsupported` with `safeParse`) stops
   recognizing the step-up. It then falls back to the generic 403 text,
   which is the prompt-swallowing this `oneOf` exists to prevent (§2, "The
   403 is one status with three disjoint bodies"). AC-22's typed outcome is
   built on exactly this schema.
   **Fix, pick one:**
   (a) Make it really open: `items: {type: string, minLength: 1}`. Name the
   known value in the description (`sso_configuration_writes`, and say a
   client ignores any other), and drop the `enum`.
   (b) Make it really closed: keep the `enum` and delete "Values may be
   added; a client ignores one it does not know" from the description. Also
   change "open enum" in §2 / Revision 5 and in spec D-11. A new value then
   becomes a contract revision with a web regeneration.
   Either fix is consistent. The contract must not state both.

2. **B2: `stepup_scopes` requires a Microsoft list on deployments with no Microsoft step-up.**
   `stepup_scopes` has `required: [google, microsoft]` and `minItems: 1` on
   each list. Its description says "Each list is exactly what
   startSsoAdminStepup requests beyond those". Microsoft is optional per
   deployment: `docker-compose.prod.yml:69` has `MICROSOFT_CLIENT_ID:
   ${MICROSOFT_CLIENT_ID:-}`, and `e2e/docker-compose.e2e.yml` sets only
   `GOOGLE_CLIENT_ID`. On such a deployment the start answers 503
   `microsoft step-up not configured` (D-2, which the contract states), so
   it requests nothing from Microsoft. The schema then forbids the honest
   answer: payloads C and D both fail (probe rows C and D). The server is
   forced to advertise scopes for a provider whose step-up cannot start. A
   client following the description explains Microsoft's admin-consent
   scopes, lets the user choose Microsoft, and navigates to a 503 JSON
   page. The mergeCondition "a test asserts both against the configured
   step-up providers" cannot be met on the e2e stack, where only Google is
   configured. The same applies to both keys when `APP_URL` is unset (D-9:
   503 for every provider).
   **Fix:** remove `google` and `microsoft` from `stepup_scopes.required`
   and keep `minItems: 1` on each list that is present. Add to the
   description: "a provider's key is present if and only if that provider's
   step-up is configured on this deployment, that is, startSsoAdminStepup
   would not answer 503 for it; an empty object means no step-up can be
   started here". Extend the scopes mergeCondition and AC-24 to cover a
   Google-only configuration (the e2e stack). AC-22's typed outcome then
   carries only the providers the user can actually pick.

### Consumers affected

- `clockwise-like` #212, `e2e/tests/sso-stepup.spec.ts:64`
  (`origin/impl/pac-50-sso-write-gate-backend` at `51aa70a`): `expect(refusal).toEqual({ error:
  'sso_write_requires_admin_stepup', stepup_url: '/api/admin/sso/stepup' })`.
  This is an exact-equality assertion on the step-up body, and the two new
  required properties break it (probe row H: that body no longer validates
  either). Lines 114-115 also assert `400` + `provider_domain_mismatch`. The
  manifest's `consumersAffected` names only the two Go test files. This
  document's "Consumers affected (re-searched 2026-10-28)" names the e2e
  file for the 400 only. Non-blocking, because e2e-gate on #212 is already a
  merge condition, but the list should be complete: add
  `e2e/tests/sso-stepup.spec.ts:64` and `:114-115`.
- `clockwise-like` #212 `backend/api/handlers_sso.go:179` and `:298` write
  `provider_domain_mismatch` with `http.StatusBadRequest`. This is covered by
  the "No 400 provider_domain_mismatch remains" mergeCondition.
- `smart-calendar-flow`: on `main`, no hand-written code references
  `/api/admin/sso`, `provider_domain_mismatch`, `SsoWrite*` or `stepup`
  (`git grep` outside `src/api/generated`). #119 regenerates cleanly against
  this bundle (above).
- `mcp/`: no reference (`grep -rl 'admin/sso\|provider_domain_mismatch\|SsoWrite' mcp`).
- PAC-22, `origin/factory/pac-22-contract` at `4901b83`: see non-blocking N2.

### Contract/implementation divergences

1. The bundle describes the post-PAC-50 gate. `main`'s `handlers_sso.go`
   still applies the suffix rule with a free-text 403. This is a
   contract-leads-stage-4 situation that every earlier round accepted. It
   is recorded here so nobody reads it as an oversight. #212 diverges as
   listed under "Consumers affected" until it merges this revision.

### Uncertainties not actually resolved

None new in revision 5. U-21 and U-22 are unchanged since rounds 1-3. The
revision-5 change touches no `x-uncertain`. Under N2, U-21's determination
stops being true once PAC-22's request schema lands first.

### Non-blocking

- **N1.** `stepup_scopes` description names `Organization.Read.All` as
  what Microsoft is asked for, while spec OQ-2's technical half is open
  (`User.Read` may suffice). Mark the named scopes as "today's", and leave
  the normative rule as "exactly what startSsoAdminStepup requests", so
  resolving OQ-2 at stage 4 does not contradict the description.
- **N2. D-12 understates what revision 6 must cover.** PAC-22's contract
  (`origin/factory/pac-22-contract` `4901b83`) does more than change the
  provider response:
  - It adds `PUT /api/admin/sso/{domain}/state` (`setSsoProviderState`). This
    is another SSO configuration write. It answers createSsoProvider's 403
    body, refuses a non-exact domain with 403 `provider_domain_mismatch`,
    and is recorded as `sso_provider_written` with `op` `suspend`/`resume`.
  - It replaces `SsoProviderCreateRequest`: OIDC only, `oidc_client_secret`
    optional, nine ordered 400 messages, and a stored-row check after the
    domain check.

  Revision 6 therefore has to revisit the following, not just "PAC-22's
  provider response":
  - `without_stepup`'s "(create, update or delete)";
  - the spec §4.6 `op` enum;
  - U-21's "only the three required fields are validated";
  - this contract's 400 list on create;
  - which operations `SsoWriteForbiddenResponse` documents.

  Today `setSsoProviderState` is not routed on #237
  (`backend/api/routes.go` has no `/state`), so nothing is ungated in the
  meantime. Widen the D-12 mergeCondition to name these items.
- **N3.** `startSsoAdminStepup` says "the step-up can be started again at
  any time (from Settings, where the callback lands)". No Settings entry
  for it exists. Spec §3 puts the SSO admin UI out of scope, and the
  manifest forbids `src/pages`. A grant is also single-use and lasts 10
  minutes, so starting from Settings with no pending write produces a
  grant that expires unused. Reword to "can be started again at any time
  by navigating to `stepup_url`". See the OWNER question below.
- **N4.** Spec header (Status line, inherited-scope U-22 line) and §3
  ("PAC-22, which lands after PAC-50 and rebases") still state the old
  order. Only §4.9 is marked superseded by D-12. §1-§2 of this document
  still describe three variants and a 400, with only the header note
  marking them as superseded.
- **N5.** `client.ts` shows a 403's `error` text as the message. A future
  SSO page must map `provider_domain_mismatch` to wording, or the raw code
  is shown. This is outside PAC-50's web scope (only `client.ts` is
  allowed), so it is noted for the SSO-page follow-up issue.
- **OWNER:** D-1 to D-5 (start for an orgless caller is 403; unconfigured
  provider is 503; the callback reason mapping; the callback 500; a 404
  delete consumes the grant) are still *assumed by the factory, pending
  owner confirmation*.
- **OWNER:** D-11 (part): `without_stepup` deliberately omits "automatic
  org membership from a verified domain", because PAC-50's step-up does
  not grant it. Confirm this reading of the 2026-10-27 answer.
- **OWNER:** the 2026-10-27 answer says the choice "can be revisited later
  in Settings". Does PAC-50 have to ship a Settings entry point for the
  step-up, or is "navigate to `stepup_url` when you next write" enough until
  the SSO admin page exists (spec §3 follow-up)?
- **OWNER:** D-12 (PAC-22 lands before PAC-50) is sequencing that is
  assumed by the factory.

### Verified clean

- **Disjointness of the four-variant `oneOf`.**
  - F `{"error":"provider_domain_mismatch"}` matches exactly one variant.
  - E (mismatch plus `stepup_url`) and G (fresh-login plus
    `without_stepup`) match none, because every variant is
    `additionalProperties: false` / `strictObject`.
  - `SsoWriteMembershipRequired`'s `enum` and the three `const`s share no
    value.
- **Domain 403 before consume.** `POST /api/admin/sso`
  `{"domain":"sub.acme.com","provider_name":"x","provider_type":"oidc"}`,
  `{"domain":" acme.com ",…}` and `{"domain":"other.com",…}` from a
  grant-holder of `acme.com` are each 403 `provider_domain_mismatch`. The
  fragment orders domain (step 3) before authorization (step 4) and says
  "Every 400 and 403 above … leaves the grant live". This matches AC-3c,
  AC-16b and spec §4.2 steps 4-5a.
- **Orgless callers.** An orgless caller sending `{"domain":"other.com",…}`
  gets the membership 403 first (step 1), not the mismatch, as AC-1b
  requires.
- **`DELETE`.**
  - The bundle's `deleteSsoProvider` has no `400` left. I could construct
    no handwritten-handler path that answers one: an invalid percent-escape
    is refused by `net/http` before routing. I checked this with a raw
    request: `DELETE /api/admin/sso/%zz` got `HTTP/1.1 400 Bad Request`
    from the server and never reached the handler. That 400 is
    transport-level and outside the operation, like any malformed request
    line.
  - `DELETE /api/admin/sso/stepup` is documented as 403
    `provider_domain_mismatch` with nothing consumed.
- **AC-18.** On neither write operation does the bundle document
  `provider_domain_mismatch` as a 400. The create `400` lists four
  messages, none of them that code.
- **Linear relayed acceptance** "sub.example.com → 403" now matches the
  contract. The revision-3 Note is correctly withdrawn, and D-10 is recorded
  as *owner, 2026-10-27* in spec §9, as owner-decisions.md requires.
- **Migration 029.** `grep '028_sso\|migration 028'` over the fragment and
  the bundle finds nothing. `SsoProviderResponse.Domain`, the manifest's
  `migration`, AC-20a and spec D-7 all say `029_sso_admin_stepup`.
- **Manifest `acceptanceTests` and spec §5.** AC-1a…AC-24 correspond one to
  one. AC-3c, AC-16b, AC-18, AC-20a, AC-22 and AC-24 carry the revision-5
  wording in both. No AC was narrowed silently.
- **Sign-in is not widened (AC-24).** The `startSsoAdminStepup`
  description and spec §4.3 both say sign-in requests none of the step-up
  scopes. That matches the owner's PAC-68 OQ-2 answer.

## Revision 6 — 2026-10-28 (response to the round-4 challenge)

Written by `contract-author` in a local session, against "Challenge, revision
5" above. Contract revision 6; the bundle hashes to
`7696ff3f99fe136c8fa42818daebee1a5539dc3dae27a8903c0015517593852a`
(`git show 76ffa0c:contracts/openapi/openapi.yaml | sha256sum`), recorded in
the manifest. Two schema changes, both in `SsoWriteStepupRequired`, and
description changes. No operation, status code or other schema changes.

### B1 — accepted, fix (a): `without_stepup` is really open

`without_stepup.items` is now `{type: string, minLength: 1}`; the `enum` is
gone. The description names the one value the server sends today
(`sso_configuration_writes`), says the list is open, and says a client
explains the codes it knows and ignores any other. Fix (b) was rejected
because D-11 expects PAC-68 to add a value. Under (b), that value would make
every client generated before it stop recognising the step-up 403, and that
is the failure B1 describes. With (a) a later value is a description change
for the server and no regeneration is forced on the web. Revision 5's
"open enum" (above) and spec §9 D-11 now say "open list". The Revision 5
text is annotated, not rewritten.

typed-openapi now emits `without_stepup: z.array(z.string().min(1)).min(1)`
(#119 `src/api/generated/schemas.ts:422`). The gen.go constant
`SsoConfigurationWrites` and the type `SsoWriteStepupRequiredWithoutStepup`
are gone from `backend/api/gen/paceday.gen.go`. Nothing on this branch or on
#212 (`origin/impl/pac-50-sso-write-gate-backend` `51aa70a`) references
either (`git grep`).

### B2 — accepted, fixed as proposed

`stepup_scopes` no longer has a `required` list. `minItems: 1` stays on each
list that is present, and `additionalProperties: false` stays, so the only
keys are `google` and `microsoft`. The description's normative rule: a
provider's key is present if and only if that provider's step-up is
configured on the deployment, that is, if `startSsoAdminStepup` would not
answer 503 for it (D-2 for a missing OAuth client, D-9 for `APP_URL` unset).
A Google-only deployment sends `{google: [...]}`, and an empty object means
no step-up can be started. An unconfigured provider is shown by an absent
key, not by an empty list, so `microsoft: []` stays invalid: a client never
has to tell "configured, asks for nothing extra" apart from "not
configured". `SsoWriteStepupRequired` and `startSsoAdminStepup` tell the
client to offer only the providers that are keys of `stepup_scopes`, and to
offer no step-up when it is empty.

AC-24 (spec and manifest) now covers three cases: Google only (the e2e
stack, no `microsoft` key), both providers, and neither (empty object).
AC-22's typed outcome offers only the listed providers. The scopes
mergeCondition requires tests for the same three configurations.

### Consumers (non-blocking): accepted

The manifest's `consumersAffected` now names #212
`e2e/tests/sso-stepup.spec.ts:64` (exact `toEqual({error, stepup_url})`;
stage 4 asserts the full body for the Google-only e2e stack) and `:114-115`
(400 to 403).

### N1 — accepted

The `stepup_scopes` description puts the normative rule first ("each list is
exactly what startSsoAdminStepup requests from that provider beyond the
identity scopes") and then gives the Google and Microsoft scope names as
"today's values, informative only", noting that OQ-2 may narrow Microsoft's.

### N2 — accepted

Spec §9 D-12, the manifest's `decisionsAssumed` D-12 and the "Order with
PAC-22" mergeCondition now list what the revision after PAC-22 lands must
cover:

1. `setSsoProviderState` behind this gate, and listed among the operations
   that answer `SsoWriteForbiddenResponse`.
2. `without_stepup`'s "(create, update or delete)" extended with suspend and
   resume.
3. The §4.6 `op` enum extended with suspend and resume.
4. U-21's determination redone against PAC-22's replaced request.
5. The create 400 list replaced (PAC-22's nine messages plus
   `unknown property`, with the stored-row check placed relative to the
   gate).

`setSsoProviderState` is not routed on #237, so nothing is ungated in the
meantime. This revision does not make those changes. PAC-22 is not on
`main`, and describing its operations here would describe code that does
not exist (factory §7 rule 2).

### N3 — accepted, with a recorded assumption

`startSsoAdminStepup` now says the step-up "can be started again at any time
by navigating to `stepup_url`". Every SSO write made without a live grant
answers the step-up 403 again, so the choice comes back at the next write.
It also says a grant is single-use and lasts 10 minutes, so the step-up is
meant for a write the user is about to make. Spec §4.3 says the same. The
owner's words in D-11 ("revisitable from Settings") are kept as given. How
PAC-50 meets them is the new spec §9 **D-13**: no Settings entry point in
PAC-50; the SSO admin page follow-up (spec §3) owns one. This is *assumed by
the factory, pending owner confirmation* (the OWNER question below). It is
the conservative reading: it adds no surface and the manifest's
`src/pages` prohibition stands.

### N4 — accepted

The spec header (Status line and the U-22 inherited-scope line) and §3 now
carry "*Superseded by §9 D-12*" next to the old order, as §4.9 already did.
In this document, §1 opens with a note that §1 and §2 describe revisions
1-4. Each place that later revisions changed is marked inline: three
variants (four since revision 5), the 400 (a 403 since revision 5), the
step-up variant's new properties, the PAC-22 order, and §5's `DELETE
/stepup` 400.

### N5 — accepted as a follow-up input

Nothing changes in PAC-50, whose web scope is `client.ts` and its test. The
SSO-page follow-up issue (spec §3) must map `provider_domain_mismatch`, and
the other `SsoWriteForbiddenResponse` codes, to user-facing wording rather
than show the code. This is recorded here for whoever files that issue.

### OWNER questions: still open, recorded

`owner-decisions.md` (read on 2026-10-28) answers none of the four, so each
stays *assumed by the factory, pending owner confirmation* in spec §9:

- D-1 to D-5 (start for an orgless caller is 403; an unconfigured provider is
  503; the callback reason mapping; the callback 500; a 404 delete consumes
  the grant).
- D-11 (part): `without_stepup` omits "automatic org membership from a
  verified domain", because PAC-50's step-up does not grant membership.
- D-13: no Settings entry point in PAC-50; navigating to `stepup_url` at the
  next write is enough until the SSO admin page exists.
- D-12: PAC-22 lands before PAC-50 (sequencing).

None of these deletes user data or widens who can see data.

### Alternatives rejected

- **B1 fix (b), a closed enum.** Rejected for the reason given under B1.
- **`stepup_scopes` keeps both keys, and an unconfigured provider gets an
  empty list.** A client cannot tell "configured, nothing extra" from "not
  configured", and an empty list breaks the `minItems: 1` that keeps a
  configured provider honest.
- **A separate `stepup_providers: [google]` array next to `stepup_scopes`.**
  It states the same fact twice, and the two could disagree. The keys of
  `stepup_scopes` already carry it.
- **Adding a Settings entry point now (N3).** It is outside the manifest's
  web scope and spec §3. With single-use 10-minute grants it would mostly
  produce grants that expire unused. That is left to the owner (D-13).
- **Doing PAC-22's revision now (N2).** Rejected for the reason given under
  N2.

### Gates run for revision 6

API. `api-gate.sh` itself was **not** run. It requires the host api checkout
(`C:\Users\jouzu\paceday\clockwise-like`) to be on this branch, and that
checkout is on `impl/pac-22-sso-secrets-backend`, which another stage owns.
Switching it under that stage was not acceptable. Instead, the same script
was run from a scratch copy with one difference: the host-side steps (tests,
coverage, the Makefile floors) point at this branch's worktree
(`C:\Users\jouzu\paceday\wt-pac50-challenge`) instead of the host checkout,
and the test database uses port 15457 (`PACEDAY_TEST_DB_PORT`). The
container step clones `factory/pac-50-contract` exactly as the gate does.
Run on `0e7beca` (script exit 0):

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

An earlier run of the container step alone, on `8c36c52` (the fragment
change), regenerated `backend/api/gen/paceday.gen.go` and
`contracts/openapi/openapi.yaml`. Both were committed as `76ffa0c`.

Web. `REGEN=1 web-gate.sh` was **not** run either. It copies regenerated
files into the host web checkout, which is on
`impl/pac-22-sso-secrets-frontend` (another stage). The regeneration was done
the way the gate does it, in the `paceday-gate` container: a clone of
`chore/regen-types-pac-50` (in a separate `/work50` directory), `make
openapi` against this branch's bundle at `76ffa0c`, and the changed files
copied into a worktree of `chore/regen-types-pac-50`
(`C:\Users\jouzu\paceday\wt-pac50-web`). It changed
`src/api/generated/{schemas.ts,schemas.types.d.ts,types.ts}` (+90/-42),
committed as `ffefc22` on https://github.com/Enach/smart-calendar-flow/pull/119.
Then
`bash /c/Users/jouzu/paceday/.claude/factory/web-gate.sh chore/regen-types-pac-50 factory/pac-50-contract`
(unchanged, no REGEN; script exit 0):

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

Zod probe (`npx tsx`) against the regenerated `schemas.ts`, using the
round-4 rows plus four new ones (`Stepup` = `SsoWriteStepupRequired`,
`Forbidden` = `SsoWriteForbiddenResponse`):

```
A both lists: Stepup=true Forbidden=true
B extra without_stepup value: Stepup=true Forbidden=true
C microsoft: []: Stepup=false Forbidden=false
D no microsoft key (Google only): Stepup=true Forbidden=true
D2 empty stepup_scopes: Stepup=true Forbidden=true
D3 unknown provider key: Stepup=false Forbidden=false
D4 without_stepup empty string: Stepup=false Forbidden=false
D5 without_stepup []: Stepup=false Forbidden=false
E mismatch+stepup_url: Stepup=false Forbidden=false
F mismatch: Stepup=false Forbidden=true
G fresh-login+without_stepup: Stepup=false Forbidden=false
H #212 body today: Stepup=false Forbidden=false
```

B and D now validate (B1, B2). C stays invalid on purpose (see B2). E, F
and G keep the `oneOf` disjoint.

Not run: e2e-gate. This branch carries no handler code. e2e-gate, including
`e2e/tests/sso-stepup.spec.ts`, remains a merge condition on #212.

## Revision 7 — 2026-10-28 (response to the review of #212)

Artifacts only. No fragment, schema or operation changes, so the bundle and
`contractHash` (`7696ff3f…852a`) are revision 6's, and the web types on
https://github.com/Enach/smart-calendar-flow/pull/119 need no regeneration.
This revision answers the four blocking findings of the #212 review that
concern the contract artifacts. It is **not challenged yet**: author and
challenger must be different agents, so the round owed on revision 6 now
covers revision 7 (manifest mergeConditions, first item).

### Finding 3 — D-13 withdrew an owner answer: accepted, fixed

`owner-decisions.md` (PAC-68 OQ-2 / PAC-50 OQ-2) says the choice "can be
revisited later in Settings". Revision 6 recorded, as a factory assumption,
that PAC-50 ships no Settings entry point. A factory assumption cannot
override an owner answer. Spec §9 D-13 now records the owner's answer and
the factory assumption is withdrawn:

- new spec AC-25 `[unit — web]` (manifest AC-25): Settings carries an entry
  point that explains the step-up's scopes and what is lost without it,
  says a grant lasts 10 minutes and covers one SSO write, offers only the
  configured providers, navigates to `stepup_url` only on go-ahead, and
  states the `stepup=granted|denied&reason=` outcome on return;
- the manifest's frontend `allowedPaths` gain `src/pages/Settings.tsx` and
  `src/components/settings` (with tests); `src/pages` stays forbidden
  otherwise, because the SSO admin page is still out of scope (spec §3);
- spec §3 and §4.3 say so.

**What this leaves open, stated rather than decided.** The contract gives
`stepup_scopes` and `without_stepup` only inside a refused write's 403.
Settings has no write to make, and making one to obtain them is not
acceptable (a caller holding a live grant would spend it; a `DELETE` would
delete). So the contract must also give a signed-in org member the step-up
URL, `stepup_scopes` and `without_stepup` without a write, by the 403's
rules (a key per configured provider; an orgless caller refused as at the
start, D-1). That is an operation-level change. The stage that wrote this
revision may not edit `contracts/openapi/paths/*.yaml`, so it is a new
mergeCondition owed by `contract-author`, challenged with the rest, then
implemented on #212 and used by the web stage for AC-25. Until then AC-25
cannot be implemented.

### Finding 2 — the PAC-22 / PAC-50 order: PAC-50's side fixed, PAC-22's side named

PAC-50's artifacts now state one order everywhere they are current: PAC-22
lands first (spec §9 D-12). The manifest's `operationsUnchanged` line, which
still said "lands after PAC-50", is rewritten. The remaining statements of
the old order are in revision-history text and are marked superseded.

PAC-22's own record agrees on the substance. Its D-16 (PR #237, *assumed by
the factory, pending owner confirmation*) lands PAC-22's projection and
validation half before PAC-50, and leaves `setSsoProviderState` unserved
until PAC-50 is on `main`. Only `contracts/features/PAC-22.yaml` on #237
(`contractBase`, `gates.contractApproval`, `followUps.rebaseOntoPac50`)
still says PAC-22 is rebased after PAC-50 merges. Three reviews of #237
have asked for it to be corrected. That file is on PAC-22's branch, not
PAC-50's, so this revision does not edit it; the D-12 mergeCondition now
names those keys, and the correction on #237 is a condition for landing
PAC-50.

Two descriptions in the fragment still say PAC-22 "lands after PAC-50":
`contracts/openapi/paths/auth.yaml` on `createSsoProvider`'s 201 (around line
853) and on `SsoProviderResponse` (around line 1633). The stage that wrote
this revision may not edit the fragment, so they are left as they are and
handed to `contract-author`: the revision written after PAC-22 lands
replaces both, because it replaces the provider response they describe
(D-12). They change no shape, status or generated type.

### Finding 4 — stale lines: accepted, fixed

- Manifest `operationsChanged` `createSsoProvider` and `deleteSsoProvider`:
  the `change` text now states the current behaviour first (403
  `provider_domain_mismatch`, no 400 for a domain mismatch on either
  operation), with the 400 of revisions 1-4 only in a closing "History"
  clause.
- Section 2 of this document names migration 029 (`029_sso_admin_stepup`).
- Manifest `consumersAffected`: the #212 test lines that asserted the 400
  are in the past tense; they assert 403 since `308a59a`.

### Finding 1 — no challenge on the final revision: not answerable here

A contract-challenger round on the final revision (now 7) is still owed and
is still the first mergeCondition. This revision changes nothing about that.
It also adds an item the challenger must weigh: the operation-level change
for AC-25 above.

### Gates

The gates were run on #212 after it merged this revision. They are in the
#212 PR body, not repeated here, because this branch carries no change a
gate can see: no Go, no fragment, no bundle.

## Challenge, revision 7 — 2026-10-28

Reviewed by `contract-challenger` (round 5) in a local session, against
`factory/pac-50-contract` at `97d62bb` (revision 7; the bundle is revision 6's,
`7696ff3f…852a`). Implementation read without checkout: #212
`origin/impl/pac-50-sso-write-gate-backend` at `44b1c5f` (it has merged `97d62bb`;
its contract files equal this branch's except 13 added lines in
`docs/specs/PAC-50.md`), and #119 `origin/chore/regen-types-pac-50` at
`ffefc22`. Owner answers read from `owner-decisions.md` (2026-10-27/28).

**Verdict**: accept-with-changes (2 blocking). The write gate as specified is
sound and #212 implements it as written (see "Verified clean"). Revision 7
cannot be the final revision, for two reasons: PAC-22 is now on `main`
(B1), and AC-25 has no operation to call (B2).

### Checks run (outputs as printed)

- `py -3 scripts/openapi_assemble.py --check` (worktree at `97d62bb`):
  `OK: bundle is in sync (101 paths)`, rc=0.
- `py -3 scripts/openapi_migration_report.py --check`:
  `OK: MIGRATION.md covers all 122 operations (4 generated)`, rc=0.
- `sha256sum contracts/openapi/openapi.yaml` and `git show HEAD:… | sha256sum`:
  both `7696ff3f99fe136c8fa42818daebee1a5539dc3dae27a8903c0015517593852a`,
  which matches the manifest's `contractHash`.
- `git merge-tree --write-tree origin/factory/pac-50-contract origin/main`:
  **CONFLICT** in `contracts/openapi/paths/auth.yaml`,
  `contracts/openapi/openapi.yaml`, `backend/api/gen/paceday.gen.go`,
  `contracts/openapi/MIGRATION.md`. The same command on #212 `44b1c5f` against `origin/main`
  conflicts in those four files plus `backend/api/handlers_sso.go`,
  `backend/storage/sso_providers.go` and `docs/factory/api-audit.md`. On the web side,
  #119 `ffefc22` against web `origin/main` conflicts in
  `src/api/generated/schemas.ts` and `schemas.types.d.ts`.
- A zod probe with `node` against the `SsoWrite*` constants of #119's
  `src/api/generated/schemas.ts` at `ffefc22` (extracted with `git show`, run with the web
  repo's `zod`). Results: A both providers `Stepup=true Forbidden=true`; B extra
  `without_stepup` value `true/true`; C `microsoft: []` `false/false`; D
  Google only (the exact body #212's e2e asserts) `true/true`; D2
  `stepup_scopes: {}` `true/true`; D3 unknown key `okta` `false/false`; E
  mismatch + `stepup_url` `false/false`; F `{"error":"provider_domain_mismatch"}`
  `false/true`; G fresh login `false/true`; H `org not found` `false/true`; I
  the pre-PAC-50 text `false/false`. The round-4 B1/B2 fixes are confirmed:
  rows B and D validate and row C does not, as the first mergeCondition
  requires.
- Not run: `api-gate.sh`, `web-gate.sh`, `e2e-gate.sh`, `go build`, `go
  test`. This branch carries no handler code. #212 is not checked out
  anywhere I may use, and the host checkouts belong to other stages. I
  checked #212's behaviour by reading its handlers at `44b1c5f`, not by
  running them. Gate item 3: no `x-uncertain` on any `/api/admin/sso*` operation or
  `Sso*` schema in the bundle (`grep`).

### Blocking

1. **B1: PAC-22 is on `main`, so the D-12 trigger has fired and revision 7
   describes a create operation that `main` no longer has.** #237 merged as
   `0da1b8c` (2026-10-28 08:51 UTC). Revision 7's bundle and #212 no longer
   merge with `main` (conflicts above). On the substance, three examples:
   - `POST /api/admin/sso` with
     `{"domain":"acme.com","provider_name":"x","provider_type":"saml"}` from
     a grant holder. Revision 7's `SsoProviderCreateRequest` permits it
     (`provider_type` enum `[oidc, saml]`, U-21 "nothing else is validated")
     and promises 201. `main`'s contract and handler answer 400
     `provider_type 'saml' is not supported` (PAC-22's ordered 400 list, in
     `main` `handlers_sso.go` `decodeSSOProviderWrite`).
   - Revision 7's 201 is `SsoProviderResponse`, which has 13 required
     PascalCase keys including `OIDCClientSecret`. `main` answers
     `ssoProviderView(p)` (PAC-22's `SsoProvider`, no secret). A client
     generated from revision 7 rejects every real 201.
   - `main`'s contract defines `PUT /api/admin/sso/{domain}/state`
     (`setSsoProviderState`, not routed yet). Its 403 is `ErrorResponse` and
     says "PAC-50: SsoWriteForbiddenResponse". Revision 7 does not gate it.
     `main`'s check 6 (a stored-row read, which can answer 400 `oidc_client_secret is
     required…`) also has no place yet in revision 7's ordered gate.

   The manifest's own D-12 mergeCondition lists the work (items 1-5). The
   revision-7 clause "PAC-22.yaml … is corrected on #237 by PAC-22's stage
   before either lands" can no longer be met: #237 landed with
   `contractBase`, `gates.contractApproval` and `followUps.rebaseOntoPac50`
   still saying "rebased after PAC-50 merges" (`main`
   `contracts/features/PAC-22.yaml:7,123,217`).

   **Fix:** revision 8 merges `main` (`0da1b8c` or later) into this branch,
   with no rebase. It then does D-12 items 1-5 in the fragment:
   - `setSsoProviderState` 403 → `$ref SsoWriteForbiddenResponse`;
   - `SsoWriteForbiddenResponse`'s description names the three write operations;
   - `without_stepup`'s `sso_configuration_writes` text gains suspend/resume;
   - `createSsoProvider`'s 201 is `SsoProvider` and its 400 list is PAC-22's nine plus
     `unknown property: <name>`;
   - check 6 is placed relative to step 4 (it is a 400 before the grant is
     consumed, or the description says otherwise);
   - `SsoProviderCreateRequest`'s U-21 text is restated against PAC-22's request.

   It replaces the two "lands after PAC-50" descriptions that revision 7
   handed over (createSsoProvider 201, `SsoProviderResponse`). It also
   restates the PAC-22.yaml clause as a follow-up owned by PAC-22's next
   stage, not a precondition. #212 and #119 then regenerate against the new
   hash.

2. **B2: AC-25 (the owner's "revisitable from Settings") has no operation
   behind it.** In this contract, `stepup_scopes` and `without_stepup` exist
   only inside the 403 of a refused SSO write. A Settings page has no write
   to make, and the normative rule on `stepup_scopes` forbids hard-coding
   it. So a client cannot implement AC-25 against this bundle: there is no
   request whose response carries the configured providers. Revision 7
   says so itself and defers it to a mergeCondition. The owner's answer is
   settled (PAC-68 OQ-2 / PAC-50 OQ-2), so the contract is incomplete
   against its own manifest. Today's only ways to get the values are both
   wrong:
   - `POST /api/admin/sso` with `{}`: a 400 for a member with no grant
     (the body check runs before the 403), so it does not even yield them.
   - `DELETE /api/admin/sso/<org domain>`: a grant holder deletes the row.

   **Fix (contract shape):**
   - Add `GET /api/admin/sso/stepup/options` (`getSsoStepupOptions`, behind
     the session). Its path does not collide with `GET /stepup`,
     `GET /stepup/callback`, `DELETE /{domain}` or `PUT /{domain}/state`.
   - 200 is a new `SsoStepupOptions`:
     `{stepup_url, stepup_scopes, without_stepup}`, all required,
     `additionalProperties: false`.
   - 401 is text/plain. 403 is `SsoWriteMembershipRequired` for an orgless
     caller (D-1).
   - Extract `SsoStepupScopes` and `SsoWithoutStepup` as components, and
     `$ref` them from both `SsoStepupOptions` and `SsoWriteStepupRequired`,
     so the two bodies cannot drift.
   - State that it consumes nothing, sets no cookie, and that its
     `stepup_scopes` equals the step-up 403's on the same deployment.
   - Extend AC-24 to cover it.

   Optional: a `breakglass: boolean` so Settings can tell a listed operator
   that the step-up is unnecessary. That is the author's call.

### Requests wrongly permitted

- See B1 (the SAML create is permitted by revision 7 and refused by `main`).
  Against #212 itself I found no request the contract permits that the
  handler refuses, apart from B1's PAC-22 drift (see "Verified clean").

### Responses the client cannot handle

- See B1: revision 7's `SsoProviderResponse` on the 201 versus `main`'s
  `SsoProvider`.
- The four-variant 403 renders. Rows A-I above are disjoint and the Go encoder
  emits `{}`, not `null`, for no provider. `stepupRequired()` uses `make(map…)`
  (`handlers_sso_stepup.go:264`).

### Consumers affected

- smart-calendar-flow `main` now carries PAC-22's generated types (`b15a15e`, #129). #119
  conflicts with it and must be regenerated after revision 8.
- smart-calendar-flow #119 contains only generated files. AC-22 (`src/api/client.ts`, which
  today renders a 403 from `error` at `client.ts:398`) and AC-25 are on no
  web branch yet. This is not a contract defect, and the mergeConditions already require
  both.
- `mcp/`: no reference to `admin/sso`, `stepup`, `SsoWrite` or
  `provider_domain_mismatch` on #212 (`git grep`).
- `e2e/`: only #212's `e2e/tests/sso-stepup.spec.ts` (`:71-75` asserts the
  Google-only body, the same as probe row D; `:131-132` asserts 403
  `provider_domain_mismatch`; `:152` asserts that sign-in requests no
  `admin.directory` scope) and `e2e/docker-compose.e2e.yml:119`.

### Contract/implementation divergences (non-blocking)

1. `startSsoAdminStepup` 400: the contract attributes the text/plain
   `Query argument provider is required, but not found` to "the generated
   request wrapper's default error handler". In fact #212 mounts the route on
   chi (`mountAdmin`) and writes that text itself
   (`handlers_sso_stepup.go:358`). The bytes are the same, but the rationale is
   wrong. Reword it.
2. `startSsoAdminStepup` order: #212 validates `provider` before
   membership (`:353-375`). So an orgless caller with `?provider=okta`
   gets 400, not D-1's 403, and an orgless caller with an unconfigured
   provider gets 403 before 503. The contract states no order. State it
   (provider 400 → membership 403 → 503 → 500).
3. Break-glass freshness: #212 also treats a session `iat` more than one
   minute in the future as stale, giving 403 `sso_write_requires_fresh_login`
   (`:238`). The contract says only "issued less than 10 minutes ago". Add
   a clause for the future case.
4. The callback always lands on `<FRONTEND_URL>/settings`, including for a
   step-up started from a refused write elsewhere. Settings must then lead
   back to the write within the grant's 10 minutes. Name this in AC-25's
   outcome text so the web stage does not drop the user there.

### Uncertainties not actually resolved

- U-21 is no longer true on `main` (B1). U-22's "13 PascalCase keys" no
  longer describes `main`'s response (B1). Both determinations must be
  redone in revision 8, not carried over.

### Owner decisions: reflected?

- **403 for a subdomain or foreign domain on SSO writes:** reflected in the
  contract (`SsoWriteDomainMismatch`, both writes, no 400 left) and in #212
  (`handlers_sso.go:181-183`, `:300-302`, `domain.DomainMatchesOrg` now
  `d != "" && strings.EqualFold(d, orgDomain)`). `setSsoProviderState` must
  follow in revision 8 (B1). `main`'s PAC-22 contract already says 403 there
  (PAC-22 D-15).
- **Extended IdP permissions are optional, explained before navigating:**
  reflected (`SsoWriteStepupRequired` description, AC-22, `stepup_scopes`
  only for configured providers, as #212 `stepupRequired()` emits).
  **Revisitable from Settings:** recorded (D-13, AC-25) but not served
  (B2).
- **Sign-in never requires a new admin consent:** reflected (startSsoAdminStepup
  description, AC-24). #212 carries `backend/auth/pac50_signin_scopes_test.go`,
  and `e2e/tests/sso-stepup.spec.ts:152` asserts it. I did not run either test.

### Verified clean

Each payload below was traced through #212 `44b1c5f` and matches the contract:

- **Domain gate.** Org `acme.com`, the caller holding one live grant:
  - `POST {"domain":"sub.acme.com",…}`, `{"domain":"other.com",…}` and
    `{"domain":" acme.com ",…}` give 403 `provider_domain_mismatch`. The
    domain check (`:181`) runs before `authorizeWrite` (`:186`), so the
    grant stays live.
  - `{"domain":"ACME.com",…}` passes, and the row is stored lower-cased (`:192`).
  - `DELETE /api/admin/sso/%20acme.com`, `/sub.acme.com`, `/other.com` and
    `/stepup` give 403 `provider_domain_mismatch` (`:299-302`).
  - `DELETE /api/admin/sso/ACME.com` reaches authorization.
- **Gate order.** Membership (`callerOrg`) → body (`decodeSSOProviderBody`:
  `invalid JSON`, `unknown property: <name>` in sorted order, required,
  enum) → domain → grant (consumed in `consumeGrant`) → break-glass → step-up
  403. This is the contract's order, including "grant tried before break-glass".
  An orgless caller sending `{"org_id":1}` gets the membership 403, not a
  400.
- **`stepup_scopes` iff configured.** A key is emitted when a provider is non-nil
  (`:265-268`). The start answers 503 exactly when that provider is nil
  (`:376-386`). Both are nil when `APP_URL`, the client id or the secret is
  unset (`ssoOptionsFromEnv`), so the D-2/D-9 rule holds. Each list is
  `ExtraScopes()`, derived from the scopes the start requests
  (`backend/auth/stepup.go:91`), so it cannot drift from the request.
- **`without_stepup`** is `["sso_configuration_writes"]` (`:274`), which is
  open-list compatible (probe row B).
- **Callback.** The reasons and their order (state → token → hd/tid →
  email → admin) match the contract and its 8-value pattern. The redirect is
  `frontendURL + "/settings?stepup=…"` (`:523-525`). The state cookie is
  cleared through `setCookie` (PAC-20). A missing user row gives JSON 401 `unauthorized`.
- **Delete 404** consumes the grant (D-5) and records nothing.

The bundle is in sync, and the hash matches the manifest.

## Revision 8 — 2026-10-28 (response to the round-5 challenge)

Written by `contract-author` in a local session, against "Challenge, revision
7" above. Contract revision 8. `origin/main` at `8c542de` (PAC-22 #237 as
`0da1b8c`, then PAC-73 #239) is **merged** into this branch as `6a1521e`, with
no rebase. The bundle at `6a1521e` hashes to
`62853fa6daf6eb91e2e013a0c53d09ef6f50c52686c94b62fec7ff9bf993a16d`
(`git show 6a1521e:contracts/openapi/openapi.yaml | sha256sum`), recorded in the
manifest. The commit that adds this section changes no fragment and no bundle.

### Merge conflicts and how they were resolved

`git merge origin/main` stopped on four files. Each is resolved so that both
sides' intent survives:

| File | `main` (PAC-22) wanted | This branch (PAC-50) wanted | Resolution |
|---|---|---|---|
| `contracts/openapi/paths/auth.yaml`, `createSsoProvider` | OIDC-only request, 12 ordered 400s, stored-row check 6, 201 `SsoProvider`, interleaved-write rules, active state kept; 403 still `ErrorResponse` with the old domain message ("PAC-50 replaces this gate") | The gate: membership, body, exact domain 403, authorization, store; 403 `SsoWriteForbiddenResponse`; `unknown property` 400 | PAC-22's request, 201, 400 messages, secret rules, interleaving and active-state text are kept word for word where they do not describe the gate. They are placed inside PAC-50's gate as steps 2 (checks 1-5, with `unknown property` as 2c) and 4 (check 6). The 403 is PAC-50's. The suffix-rule 403 text is gone, as PAC-22 said it would be. |
| same file, `listSsoProviders` | `SsoProviderList` of `SsoProvider`, generated server | untouched by PAC-50 | `main`'s, unchanged. |
| same file, components | `SsoProviderResponse` removed; `SsoProvider`, `SsoProviderList`, `SsoProviderState`, `SsoProviderStateRequest` added; new `SsoProviderCreateRequest` | `SsoProviderResponse` (13 PascalCase keys) and the old request, with PAC-50 descriptions; the `SsoWrite*` schemas | `main`'s schemas, with PAC-50's descriptions added where PAC-50 changes behaviour (`domain` on the request: 403 and lower-casing; `SsoProvider.domain`: lower-cased, migration 030; `unknown property` on the request). `SsoProviderResponse` stays removed. PAC-50's `SsoWrite*` schemas are kept. |
| same file, `setSsoProviderState` (auto-merged from `main`, then edited) | Specified "against PAC-50's gate", 403 `ErrorResponse`, a note that PAC-50's branch still answered 400 | n/a | Put behind the gate (B1 below). The obsolete note about PAC-50's 400 is removed, because the owner's 403 is what PAC-50 says. |
| `contracts/openapi/MIGRATION.md` | 121 operations, 5 generated; `listSsoProviders` generated; PAC-22 notes on `createSsoProvider`; `setSsoProviderState` row | 122 operations, 4 generated; step-up rows | `main`'s rows and notes, plus PAC-50's two step-up rows and the new `getSsoStepupOptions` row, then `openapi_migration_report.py` rewrote the counts: 124 operations, 5 generated, 119 handwritten. |
| `contracts/openapi/openapi.yaml` | (generated) | (generated) | Not hand-merged: reassembled from the resolved fragments by `scripts/openapi_assemble.py`. |
| `backend/api/gen/paceday.gen.go` | (generated) | (generated) | Not hand-merged: regenerated by `scripts/openapi_gen_go.sh` (oapi-codegen v2.4.1). `go build ./...` passes. `main`'s `generatedServer` embeds the strict interface, so the two new methods (`GetSsoStepupOptions`, and `SetSsoProviderState` already on `main`) need no stub. |

`auth.notes.md` merged without a conflict; its section (d) is updated for this
revision. `docs/specs/PAC-50.md` merged without a conflict and is updated (see
below).

**A second conflict the merge did not show: migration 029.** PAC-73 landed
`029_llm_bring_your_own`. PAC-50's migration was `029_sso_admin_stepup`, so it
moves to **`030_sso_admin_stepup`** (spec §9 D-7, the manifest's `migration`,
AC-20a, `SsoProvider.domain`). It touches no table 029 alters: 029 alters the
settings and LLM tables, and 030 touches `sso_providers`, `organizations` and
its own two new tables.

### B1 — accepted, fixed as proposed

The D-12 trigger fired, and every item the challenge listed is now in the
fragment:

- **`setSsoProviderState` 403 is `$ref SsoWriteForbiddenResponse`.** Its step 3
  is the `SsoWriteDomainMismatch` variant, and its step 4 is
  `createSsoProvider`'s authorization step (a grant tried first, then
  break-glass with a fresh session). Its record carries `via`. Its description
  now says it is **not served before PAC-50 is on `main`** (PAC-22 D-16), so
  no SSO write is ever served ungated. Serving it is PAC-22's follow-up, and
  that follow-up inherits this gate (manifest `mergeConditions`).
- **`SsoWriteForbiddenResponse`'s description names the three writes.**
  `SsoWriteDomainMismatch` names the `{domain}` of `setSsoProviderState`.
- **`without_stepup`.** The `sso_configuration_writes` code now reads "create,
  replace, delete, suspend or resume" (`SsoWithoutStepup`).
- **Spec §4.6.** The `op` enum is now `create|update|delete|suspend|resume`.
- **`createSsoProvider`'s 201 is `SsoProvider`.** The 400 list is PAC-22's 12
  messages plus `unknown property: <name>`, listed in gate order. (The
  challenge said "nine"; the list on `main` has twelve, counting the three SAML
  refusals and the three secret messages.)
- **Check 6 placed.** It is gate step 4: after the domain 403 and before the
  grant is consumed, so it is a 400 that leaves the grant live (AC-3c's
  intent: a mistyped issuer does not burn a grant). It reads the row under the
  lower-cased domain, which PAC-22's own text had anticipated ("after PAC-50,
  … the row under the lower-cased domain"). Its read failure is a 500 before
  any consume. PAC-22's *store-time* refusal (its D-14) happens after the
  consume, so it spends the grant, as a storage failure does (§4.2 step 5a).
  PAC-22 left this open, and it is now spec §9 D-14(2).
- **U-21 restated.** PAC-22 resolved U-21 on `main` with its replaced request.
  Revision 8 redetermined it by reading `main`'s `decodeSSOProviderWrite`
  (`backend/api/handlers_sso.go`): it validates exactly what the schema and
  the 400 list state. PAC-50 adds only the `unknown property` refusal, so
  `additionalProperties: false` is now what the server does. **U-22**
  (13 PascalCase keys) no longer describes anything: PAC-22 removed
  `SsoProviderResponse`. The manifest's `resolvesUncertain` says so instead of
  carrying the old determination forward.
- **The two "lands after PAC-50" descriptions** handed over by revision 7
  (`createSsoProvider` 201 and `SsoProviderResponse`) are gone, because the
  text they were in was replaced.
- **The `PAC-22.yaml` clause** is no longer a precondition. PAC-22 landed with
  `contractBase`, `gates.contractApproval` and `followUps.rebaseOntoPac50`
  still stating the old order. It is now a manifest `followUps` item owned by
  PAC-22's next stage.
- #212 and #119 regenerate against the new hash after this revision is
  challenged (manifest `mergeConditions`). Neither was touched here.

One more consequence of the merge, settled here rather than left for the
challenger. On PAC-22's `main`, `org not found` came after the body 400s.
PAC-50's gate decides membership, and with it `org not found`, before the
body is read. Revision 8 keeps PAC-50's order (spec §9 D-14(1)), because a
caller whose org row is gone has no org to write for, whatever the body
says. #212's `callerOrg` already does this.

### B2 — accepted, fixed as proposed (without the optional flag)

- **`GET /api/admin/sso/stepup/options`** (`getSsoStepupOptions`), behind the
  session. Its path is distinct from `GET /stepup`, `GET /stepup/callback`,
  `DELETE /{domain}` and `PUT /{domain}/state`. `{domain}` matches one segment,
  so `/stepup/options` can only be confused with `/{domain}/state` if the last
  segment were `state`, and it is not.
- **200 `SsoStepupOptions`** = `{stepup_url, stepup_scopes, without_stepup}`,
  all required, `additionalProperties: false`.
- **401** text/plain. **403** `SsoWriteMembershipRequired` (`org membership
  required`, or `org not found` when the org row is gone, as at the writes'
  step 1). It has no 500: membership lookup errors are the 403, as for every
  SSO operation, and the configured providers are in memory.
- **Shared components.** `SsoStepupScopes`, `SsoWithoutStepup` and also
  `SsoStepupUrl` are extracted, and `SsoWriteStepupRequired` and
  `SsoStepupOptions` both `$ref` them, so the two bodies cannot drift.
  `SsoStepupUrl` was extracted too, because the same drift argument applies to
  the `const`.
- **Stated in the description.** It consumes and consults no grant, sets no
  cookie, records nothing, and its `stepup_scopes` and `without_stepup` equal
  the step-up 403's on the same deployment. It also says that the
  `sso_stepup_state` cookie's `Path=/api/admin/sso/stepup` covers this path,
  so the browser sends that cookie here mid-step-up, and the operation ignores
  it.
- **AC-24** is extended to the options operation on the three configurations.
  **AC-26** `[unit]` (new) pins the operation itself.
- **AC-25** is now fully specified for the web stage. The values come from this
  operation. Its 403 means no entry point. The return states the outcome:
  `granted` says the grant is live for 10 minutes and is spent by the next SSO
  configuration write, so the user should make that write now (this is
  divergence 4). Settings makes no write and starts nothing on return. An
  unknown `reason` is shown as a denial without detail. No `stepup`, or any
  other value, shows nothing.
- **AC-22** needed no new contract surface: the 403 carries everything it
  needs, and its manifest text is unchanged. **The web lacks both AC-22 and
  AC-25.** That is stage-4 work, and the manifest's web `mergeConditions` item
  now names both, with their files.

**`breakglass: boolean` — rejected.** Break-glass needs a session less than 10
minutes old, so the flag would go stale before the user acts on it. The
write's own 403 (`sso_write_requires_fresh_login`) already tells a listed
operator what to do. It would also turn a read-only, caller-independent answer
into one that depends on the caller (spec §9 D-14(6)).

### Contract/implementation divergences (non-blocking): all applied

1. **`startSsoAdminStepup` 400 rationale.** It is reworded: the route is mounted
   directly, and the handler writes the generated wrapper's bytes itself, so the
   body does not change if the operation moves to the generated server. Spec §9
   D-14(5).
2. **Start order stated.** It is `provider` 400, then membership 403, then 503,
   then 500, with the two examples from the challenge. The 403 is
   `org membership required` also for a missing org row (#212 does that). Spec
   §9 D-14(3).
3. **Future `iat`.** A session issued more than one minute in the future is not
   fresh. This is in `createSsoProvider` step 5b and in
   `SsoWriteFreshLoginRequired`. Spec §9 D-14(4).
4. **Callback lands on Settings.** This is in AC-25's outcome text (above) and
   in the callback description ("a step-up started from a refused write
   elsewhere also lands there").

### Uncertainties not actually resolved: answered

U-21 and U-22 are restated under B1 and are not carried over. No `x-uncertain`
marker is on any `/api/admin/sso*` operation or `Sso*` schema in the bundle
(checked with a short script over the bundle). The bundle's `x-uncertain`
count is `main`'s (7 lines).

### Owner decisions: still reflected

- **403 for a subdomain or foreign domain** applies to all three writes,
  `setSsoProviderState` included.
- **The optional step-up, explained before navigating, revisitable from
  Settings** (PAC-68 OQ-2 "offer both") is now served: the
  `getSsoStepupOptions` operation backs AC-25.
- **Sign-in never asks for a new admin consent**: unchanged (AC-24).
- **LLM org key OQ-1** (2026-10-28) reuses PAC-50's admin proof. Nothing in
  this revision blocks that. The grant is consumed by "the next SSO
  configuration write", and whether an org-key write may also consume it is
  that ticket's contract to say. This revision does not widen the grant's
  scope beyond SSO configuration writes.

No question for the owner: everything this revision decides is an interface
detail, recorded as such in spec §9 D-13/D-14.

### Alternatives rejected

- **Rebasing onto `main`.** Published history is never rebased (factory,
  spec §9 D-12).
- **Keeping PAC-22's order with `org not found` after the body.** It would make
  the two membership messages straddle the 400s, so an org-less caller could
  learn whether a body is well-formed. PAC-50's gate decides membership first.
- **`unknown property` before the SAML refusals.** A SAML property would then be
  reported as unknown, which loses PAC-22's named refusal (PAC-22 AC-18).
- **Check 6 after authorization.** A mistyped issuer would burn a grant, which
  is the AC-3c failure the gate order exists to prevent.
- **`/api/admin/sso/stepup-options`**, to keep the state cookie off the options
  path. Rejected: the cookie is harmless there and ignored. The challenger's
  path keeps all step-up operations under one prefix.
- **Serving the options inside `listSsoProviders`'s response.** That read is
  PAC-22's, and it is unrelated to the step-up. It would also change a response
  the web already consumes.
- **A `breakglass` flag:** see B2.

### Gates run for revision 8

Run in the worktree `C:\Users\jouzu\paceday\wt-pac50-contract` on `6a1521e`,
before the commit that adds this section (which changes only this file and
the manifest):

```
$ python3 scripts/openapi_assemble.py --check
OK: bundle is in sync (103 paths)                                   rc=0
$ python3 scripts/openapi_migration_report.py --check
OK: MIGRATION.md covers all 124 operations (5 generated)            rc=0
$ bash scripts/openapi_gen_go.sh --check
OK: backend/api/gen matches the contract                            rc=0
$ (cd backend && go build ./...)                                    rc=0
$ git show 6a1521e:contracts/openapi/openapi.yaml | sha256sum
62853fa6daf6eb91e2e013a0c53d09ef6f50c52686c94b62fec7ff9bf993a16d
```

The same three checks were rerun with `py -3` on the final commit, after this
section was added (see the commit message of that commit).

**Not run:** `api-gate.sh`, `web-gate.sh`, `e2e-gate.sh` and `go test`. There
is no test database on this host: `go test ./api -run PAC21` failed at the
test database connection (`127.0.0.1:15432` refused), so
`pac21_contract_5xx_test.go` did not run against the new bundle. This branch
carries no handler code. No zod or JSON-Schema probe of the new
components was run: the web types were not regenerated here (#119 is not
touched, as instructed), and no JSON-Schema validator is installed in this
Python. The challenger should repeat the round-5 probe rows A-I against #119
after it regenerates, and add one for `SsoStepupOptions`.

## Challenge, revision 8 — 2026-10-28

Reviewed by `contract-challenger` (round 6) in a local session, in a fresh detached
worktree of `origin/factory/pac-50-contract` at `5598dc6` (revision 8; the merge
`6a1521e` brings `origin/main` `8c542de`, PAC-22 #237 and PAC-73 #239). `main`'s
PAC-22 code and tests were read with `git show origin/main:…`. #212
(`origin/impl/pac-50-sso-write-gate-backend` `44b1c5f`) was read without checkout. It
has not yet merged revision 8. Owner answers were read from `owner-decisions.md`.

**Verdict**: accept. Both blocking findings of round 5 are fixed as asked, and the
four divergences are applied. I found no request that reaches an SSO write without
the admin proof. Every grant spent on a refused request is a documented,
deliberate case. The findings below are non-blocking. N1 and N2 should be fixed
before #212 is updated to this revision, because each costs one sentence.

### Checks run (outputs as printed)

All checks ran in the worktree at `5598dc6`:

- `py -3 scripts/openapi_assemble.py --check`: `OK: bundle is in sync (103 paths)`, rc=0.
- `py -3 scripts/openapi_migration_report.py --check`: `OK: MIGRATION.md covers all 124 operations (5 generated)`, rc=0.
- `bash scripts/openapi_gen_go.sh --check`: `OK: backend/api/gen matches the contract`, rc=0.
- `cd backend && go build ./...`: rc=0, with no output.
- `sha256sum contracts/openapi/openapi.yaml` gives
  `62853fa6daf6eb91e2e013a0c53d09ef6f50c52686c94b62fec7ff9bf993a16d`, which equals the
  manifest's `contractHash`.
- `main`'s PAC-21 bundle test (`backend/api/pac21_contract_5xx_test.go`) was run in
  isolation. I copied it into a scratch Go module, pointed the bundle path at this
  worktree, and ran `go test -v`. Both tests passed:
  `TestPAC21Every5xxInTheContractIsTheJSONEnvelope` and
  `TestPAC21ContractCheckCatchesBadShapes`. rc=0. This covers the run the author
  could not make, because the package's own TestMain needs a database.
- An ajv 8.20 (draft 2020-12) probe ran against the bundle's `components.schemas`,
  loaded with js-yaml from the web repo's `node_modules`. Each row shows valid (V)
  or invalid (I):
  - `SsoStepupOptions`:
    - O1, both providers: V
    - O2, `stepup_scopes: {}`: V
    - O3, `stepup_scopes: null`: I
    - O4, `{microsoft: []}`: I
    - O5, with an `error` key: I
    - O6, absolute `stepup_url`: I
    - O7, extra `breakglass`: I
  - `SsoWriteForbiddenResponse`:
    - F1, the step-up 403: V
    - F2, the options body sent as a 403: I. The two bodies cannot be confused.
    - F3, `org not found`: V
    - F4, `provider_domain_mismatch`: V
    - F5, the pre-PAC-50 text `can only configure SSO for your own domain`: I
  - `SsoProviderCreateRequest`:
    - C1, `provider_type: saml`: I
    - C2, `saml_cert: ""`: I
    - C3, `org_id`: I
    - C4, `oidc_client_secret: null`: I
    - C5, a replace with no secret: V
    - C6, `sub.acme.com`: V. This is correct, because the 403 is a server decision.
  - `SsoProviderStateRequest`:
    - S1, `{state:"active"}`: V
    - S2, an extra `oidc_issuer`: I
    - S3, `state: null`: I
- Bundle walk over `/api/admin/sso*`:
  - The three writes' 403s are all `SsoWriteForbiddenResponse`.
  - The 201 is `SsoProvider`.
  - `getSsoStepupOptions` answers 200 `SsoStepupOptions`, 401 text/plain and 403
    `SsoWriteMembershipRequired`.
  - `SsoProviderResponse` is absent, and no `x-uncertain` sits on any of these
    operations.
- Migration numbering: across every remote branch, no `030_*` exists. The only
  `029_*` other than `main`'s `029_llm_bring_your_own` is #212's
  `029_sso_admin_stepup`, which the manifest already requires to be renamed.
- Not run: `api-gate.sh`, `web-gate.sh`, `e2e-gate.sh`, `go test ./api`. There is
  no Docker and no database, and this branch carries no handler code. #119 has not
  been regenerated, so no zod probe of the regenerated web types was possible. The
  ajv probe above stands in for it at the schema level only.

### Round-5 findings: applied?

- **B1. Applied.**
  - `setSsoProviderState` 403 is `$ref SsoWriteForbiddenResponse`. Its order is
    membership → body → domain 403 → authorization → 404. A diff against `main`'s
    fragment shows only gate text changed.
  - `SsoWriteForbiddenResponse` names the three writes.
  - `SsoWithoutStepup` reads "create, replace, delete, suspend or resume".
  - Spec §4.6 `op` has `suspend|resume`.
  - The 201 is `SsoProvider`.
  - The 400 list is `main`'s twelve messages plus `unknown property`.
  - Check 6 is step 4, before the grant.
  - U-21 and U-22 are restated, not carried over.
  - The PAC-22.yaml clause is a `followUps` item.
  - `main` really does route `setSsoProviderState` nowhere. Its `generatedServer`
    mounts only named methods (`handlers_credentials.go:24-29`, `:62`), so no
    ungated route exists for it.
- **B2. Applied.**
  - `GET /api/admin/sso/stepup/options` exists.
  - `SsoStepupOptions` is closed, and all three keys are required.
  - `SsoStepupUrl`, `SsoStepupScopes` and `SsoWithoutStepup` are shared by
    `$ref` from both bodies.
  - The read-only and no-cookie rules are stated.
  - AC-24 is extended, and AC-26 is added in both the spec and the manifest.
  - The decision to reject `breakglass` is justified and recorded (D-14(6)).
- **Divergences 1-4. Applied.**
  - 1: the start's 400 rationale is reworded.
  - 2: the start order is stated.
  - 3: the future-`iat` clause is in step 5b and in `SsoWriteFreshLoginRequired`.
  - 4: AC-25 and the callback description say the user lands on Settings.

### Can any SSO write be reached without the admin proof, or spend a grant on a refused request?

**Writes reachable without the proof.** I traced each payload below through the
revision-8 text. None is permitted.

- `POST /api/admin/sso` from an org member with no grant, with
  `{"domain":"acme.com","provider_name":"x","provider_type":"oidc","oidc_issuer":"https://i","oidc_client_id":"c","oidc_client_secret":"s"}`,
  gives 403 `sso_write_requires_admin_stepup`.
- `PUT /api/admin/sso/acme.com/state` with `{"state":"suspended"}`: no route
  answers it in PAC-50, and once served it gets the same step 4.
- `DELETE /api/admin/sso/ACME.com` with no grant gives 403.
- `PUT /api/admin/sso/stepup/state` gives 403 `provider_domain_mismatch`.

**Requests that spend a grant and are then refused.** Each case is documented and
deliberate:

- A delete that finds no row (404, D-5).
- A state change that finds no row (404, PAC-22 D-9).
- The store-time 400 of `createSsoProvider` step 6 (D-14(2)). This is reachable
  only when another write to the same domain lands between check 6 and the store.
- Every 500 after the consume.
- A break-glass caller who also holds a grant (grant tried first).

Every 400 and the domain 403 come before the consume.

**Can check 6, run before authorization, be used as an oracle?** No. A grantless
member who sends a replace with no secret and a guessed issuer learns `…required
when oidc_issuer or oidc_client_id changes` instead of the step-up 403. But
`listSsoProviders` already gives any member `oidc_issuer`, `oidc_client_id` and
`has_oidc_client_secret`, so check 6 discloses nothing new.

### Non-blocking

1. **N1. Step 2a contradicts step 2f and the schema on `null`.** Revision 8 adds
   to step 2a: "each declared property present has its declared type (`invalid
   JSON`)". `null` is not a string, so two payloads get two answers:
   - `POST /api/admin/sso` with
     `{"domain":"acme.com","provider_name":"x","provider_type":"oidc","oidc_issuer":"https://i","oidc_client_id":"c","oidc_client_secret":null}`:
     step 2a says `invalid JSON`. Step 2f, and the `oidc_client_secret` property
     description, say `oidc_client_secret must not be empty`. `main`'s
     `decodeSSOProviderWrite` answers the latter, because `json.Unmarshal` of
     `null` into `*string` is not an error.
   - `{"domain":null,…}`: step 2a says `invalid JSON`. `main` answers `domain,
     provider_name and provider_type are required`, because `null` into `string`
     is a no-op.

   `main`'s PAC-22 text had no such clause. Its check 1 was "The body is JSON".

   **Fix:** reword step 2a to "each declared property present is `null` or a value
   of its declared type; `null` counts as empty (steps 2d and 2f)".
2. **N2. Consumer not named: `main`'s `backend/api/pac22_sso_secrets_test.go`.**
   It contains 52 `/api/admin/sso` occurrences, sent through `pac22Router`, which builds
   `&ssoHandlers{ah: …}` directly (`:72-79`), as an org member who holds no
   grant. `:549-556` asserts 403 `can only configure SSO for your own domain`.
   Once #212 merges this branch:
   - every expected 201 becomes a 403;
   - the literal `ssoHandlers` lacks #212's store and clock;
   - the `:556` assertion is obsolete. The order it pins, checks 1-5 before the
     domain 403, still holds under revision 8.

   This file is the only test of PAC-22's AC-1, AC-7 and AC-22 to AC-28.
   "api-gate rc=0" will force a change to it, but nothing says the change must
   keep its assertions.

   **Fix:** add to `consumersAffected` and `mergeConditions`: "#212 adapts
   `pac22_sso_secrets_test.go` by giving its caller a live grant (or break-glass)
   and constructing the handler through #212's constructor. It keeps every PAC-22
   assertion, and changes only `:556` to 403 `provider_domain_mismatch`."
3. **N3. The spec body lags D-14.** Spec §4.2 still has these gaps:
   - Step 3 says "Body shape (create only)", but `setSsoProviderState` has a body
     too.
   - It has no step for PAC-22's stored-row check.
   - Step 5a says "every 400 and 403 above happens before it and leaves the grant
     live". D-14(2) adds a store-time 400 that comes after the consume.

   The contract is right and D-14 records the change, but the plan is written
   from §4.2. AC-3c also pins only the domain 403. The check-6 400 that leaves
   the grant live, and the store-time 400 that spends it, are pinned only by a
   `mergeConditions` line, not by an AC.

   **Fix:** amend §4.2 to match, and add AC-3e and AC-3f, or extend AC-3c.
4. **N4. Two 403 shapes for the same membership refusal.**
   - `startSsoAdminStepup`'s 403 is `ErrorResponse`, and it says `org membership
     required` even when the org row is gone (D-14(3)).
   - `getSsoStepupOptions` and the writes use `SsoWriteMembershipRequired`,
     which can also carry `org not found`.

   Both render, and AC-25 treats any options 403 as "no entry point". Pointing
   the start's 403 at `SsoWriteMembershipRequired` would give a generated client
   one type. This is optional.
5. **N5. Grant purpose under the owner's 2026-10-28 LLM-org-key answer.** The
   owner settled that setting the org AI key "requires PAC-50's admin proof …
   repeated per change". The §4.8 grant table
   `(id, user_id, org_id, provider, issued_at, expires_at, consumed_at)` has no
   purpose column, and the consume takes "the caller's newest live grant for
   (caller, org)". If the org-key ticket reuses the table, a grant obtained for
   an SSO write can be spent by an org-key write, or the reverse. AC-25's "spent
   by the next SSO configuration write" then becomes untrue.

   Revision 8 rightly does not widen the scope. However, migration 030 is
   unapplied everywhere today, and a `purpose` column (default
   `sso_configuration`) costs nothing now but needs a new migration later.

   **Fix:** decide it in the plan, or record it as a manifest `followUps` item for
   the org-key ticket.
6. **N6. The suspend and resume wording runs ahead of what is served.**
   `without_stepup`'s text and the Settings copy in AC-25 name suspend and resume.
   Under PAC-50 no route serves them, and no web page makes any SSO write (the
   SSO admin page is out of scope). This is harmless, because it is
   forward-compatible. The web stage should not show a "suspend now" call to
   action.

### Consumers

- `mcp/` and `e2e/` on `main`: neither references `admin/sso`. `git grep` hits only
  `backend/api/{handlers_sso.go,routes.go,pac22_sso_secrets_test.go}`; see N2.
- smart-calendar-flow `main` `8748c09`: nothing outside `src/api/generated`
  references `admin/sso`, `SsoWrite` or `stepup`. `src/pages/Settings.tsx`
  exists, and is routed at `/settings` (`src/App.tsx:86`), for AC-25. The 403
  branch AC-22 extends is `src/api/client.ts:398`. #119 (`ffefc22`, revision 6)
  must merge web `main` and regenerate, as the manifest says.
- #212 `44b1c5f`:
  - It has not merged revision 8.
  - `mountAdmin` (`handlers_sso_stepup.go:189-195`) lacks `/stepup/options`.
  - The migration is still `029_sso_admin_stepup`.
  - It conflicts with `main`'s PAC-22 `createSSOProvider` and
    `decodeSSOProviderWrite`.

  All four are stage-4 work already listed in `mergeConditions`, except the test
  file in N2. #212's `callerOrg` gives `org not found` before the body, as D-14(1)
  states. Its `authorizeWrite` tries the grant first and treats an `iat` more than
  1 minute in the future as stale, as revision 8 states.

### Owner decisions: reflected?

- **Subdomain or foreign domain → 403 (RFC 9110):** applied on all three writes,
  `setSsoProviderState` included. No 400 `provider_domain_mismatch` remains in the
  bundle.
- **Optional step-up, explained first, revisitable from Settings:** served by
  `getSsoStepupOptions` (AC-25 and AC-26). Sign-in scopes are unchanged (AC-24).
- **LLM org key OQ-1:** this revision does not contradict it. See N5 for the
  grant-sharing question it leaves to the plan.

## Revision 8.1 — 2026-10-28 (response to the round-6 challenge, non-blocking)

Written by `contract-author` in a local session, in a detached worktree
(`wt-pac50-81`) of `origin/factory/pac-50-contract` at `edcbe72`. The round-6
verdict was accept; this revision applies its six non-blocking findings before
#212 and the web PR are updated, as the challenger asked for N1 and N2. The
fragments, the bundle and the generated Go code are in `f71a91a`; the bundle
there hashes to
`2f030a443a4212a4d24ddfa3503df71d60f7436e442a25b6bd04ee5a1ad1efc2`
(`git show f71a91a:contracts/openapi/openapi.yaml | sha256sum`), recorded in
the manifest. The commit that adds this section changes no fragment and no
bundle.

### Findings and what changed

| Finding | Fix | Where |
|---|---|---|
| N1 step 2a vs 2f on `null` | Step 2a now reads "each declared property present is `null` or a value of its declared type"; `null` counts as empty and is decided by step d or f. `"domain":null` gives `domain, provider_name and provider_type are required`, `"oidc_client_secret":null` gives `oidc_client_secret must not be empty`, exactly as `main`'s `decodeSSOProviderWrite` (`null` into `string` is a no-op, into `*string` is nil, and neither is an unmarshal error). | `createSsoProvider` step 2a; spec §4.2 step 3; §9 D-15(1) |
| N2 `pac22_sso_secrets_test.go` not named | Named in `consumersAffected` and `mergeConditions`: #212 builds the handler through its constructor and gives the caller a live `sso_write` grant (or fresh break-glass) before each write; every PAC-22 assertion stays; only the pre-PAC-50 403 `can only configure SSO for your own domain` becomes 403 `provider_domain_mismatch`. | manifest; §9 D-15(2) |
| N3 spec body lags D-14 | §4.2 step 3 covers create and `setSsoProviderState` bodies; new step 4b is PAC-22's stored-row check (before authorization; a 500 on its read is also before any consume); step 5a lists what is decided before the consume (every 400 and 403 of steps 2-4b) and what comes after it and spends the grant (storage failure, 404 of a delete or state change, PAC-22's store-time 400). AC-3c is extended with the check-6 400 that leaves the grant live and the store-time 400 that spends it. | spec §4.2, AC-3c; manifest AC-3c |
| N4 two 403 shapes for membership | Cheap, so done: `startSsoAdminStepup`'s 403 is `$ref SsoWriteMembershipRequired` (it still always says `org membership required`; the bytes do not change). A generated client now has one type for every membership refusal under `/api/admin/sso`. | start 403; `SsoWriteMembershipRequired` description |
| N5 grant purpose | Migration 030's grant table gains `purpose TEXT NOT NULL` with no default, so every insert names it; the index serves the newest live grant for (user, org, purpose). PAC-50 issues and consumes only `sso_write`. The future organisation AI key write (owner 2026-10-28, LLM org key OQ-1) adds its own value, so neither write can spend the other's grant and no further migration is needed. New AC-27. | spec §4.1, §4.2 step 5a, §4.8; `createSsoProvider` step 5a; callback step 5; manifest `migration`, AC-27 |
| N6 suspend/resume ahead of what is served | `SsoWithoutStepup` names create, replace and delete, says suspend and resume join only when PAC-22's follow-up serves `setSsoProviderState`, and says a client must not offer them before then. AC-25's copy says the grant is spent by the next create, replace or delete, and Settings names and offers no suspend or resume. §4.6 notes PAC-50 never records them itself. | `SsoWithoutStepup`; spec AC-25, §4.6; manifest AC-25 |

### Alternatives rejected

- **A default on `purpose`** (the challenger offered `sso_configuration`). A
  default would let a later feature's insert that forgets the column mint an
  SSO grant. No default makes the omission an error.
- **A `CHECK (purpose IN ('sso_write'))`.** It would force the org-key ticket
  to write a migration only to widen it, which is what N5 asked to avoid.
- **A `purpose` parameter on the step-up start now.** Under PAC-50 every
  step-up is for an SSO write. How a step-up for another purpose is started
  is the org-key ticket's contract (spec §9 D-15(5)).
- **Leaving the start's 403 on `ErrorResponse` (N4 is optional).** The swap
  changes no bytes, only the generated type, so there was no reason to keep
  two shapes.

### Gates run for revision 8.1

Run in `wt-pac50-81` on `f71a91a`:

```
$ py -3 scripts/openapi_assemble.py --check
OK: bundle is in sync (103 paths)                                   rc=0
$ py -3 scripts/openapi_migration_report.py --check
OK: MIGRATION.md covers all 124 operations (5 generated)            rc=0
$ bash scripts/openapi_gen_go.sh --check
OK: backend/api/gen matches the contract                            rc=0
$ (cd backend && go build ./...)                                    rc=0
$ git show f71a91a:contracts/openapi/openapi.yaml | sha256sum
2f030a443a4212a4d24ddfa3503df71d60f7436e442a25b6bd04ee5a1ad1efc2
```

`api-gate`, `web-gate` and `e2e-gate` are run on #212 and the web PR after
they merge this revision; their results are recorded in those PRs, not here.

## Challenge, revision 8.1 — 2026-10-28

**Verdict**: accept

Scope: `git diff edcbe72 c61d3e0` (round-6 non-blocking N1-N6), read against
`main`'s PAC-22 decoder and against #212 at 96f24c7.

### Requests wrongly permitted
None. `POST /api/admin/sso` with `{"domain":null,"provider_name":"x","provider_type":"oidc","oidc_issuer":"https://i","oidc_client_id":"c","oidc_client_secret":"s"}`
→ 400 `domain, provider_name and provider_type are required`; with
`"oidc_client_secret":null` → 400 `oidc_client_secret must not be empty`. Both
are what `main`'s `decodeSSOProviderWrite` does (null into `string` is "", into
`*string` is nil with the key present), and #212 keeps that decoder unchanged
apart from the unknown-property step (N1).

### Responses the client cannot handle
None. `startSsoAdminStepup` 403 moves from `ErrorResponse` to
`SsoWriteMembershipRequired`; the bytes are the same (`{"error":"org membership required"}`),
and no hand-written consumer reads it (N4). The web types at
smart-calendar-flow 4166329 regenerate from this bundle with no drift
(`scripts/openapi-gen.sh --check` rc=0 against 96f24c7's bundle).

### Consumers affected
- backend/api/pac22_sso_secrets_test.go (N2): adapted on #212 by giving each
  writer a live `sso_write` grant. Three PAC-22 assertions change, not one as
  consumersAffected and spec §9 D-15(2) say: the suffix-rule 403 becomes
  `provider_domain_mismatch`; case-variant secret keys become 400
  `unknown property: <name>` (step 2c, stricter, stored secret still asserted
  unchanged); an upper-case domain now replaces the lower-case row keeping its
  secret (the contract's lower-casing). Each is required by this contract and
  none weakens PAC-22's guarantee. Non-blocking: correct the wording in the
  next revision.

### Contract/implementation divergences
None found on #212 at 96f24c7: grant `purpose TEXT NOT NULL` with no default,
index (user_id, org_id, purpose, issued_at DESC), consume matching
`purpose = 'sso_write'`, the callback issuing `sso_write` (N5, AC-27, tested in
both directions); spec §4.2 step 4b matches D-14, with the stored-row 400 before
the consume and the store-time 400 after it (N3, tested); `setSsoProviderState`
not routed; the web names and offers no suspend or resume (N6, tested).

### Uncertainties not actually resolved
None introduced.

### Verified clean
- The API surface changes only in descriptions and in the start 403 `$ref`.
  `git show c61d3e0:contracts/openapi/openapi.yaml | sha256sum` =
  2f030a44…efc2 = contractHash (same at f71a91a).
- Checks on #212 96f24c7 (which carries this bundle unchanged):
  openapi_assemble --check rc=0, openapi_migration_report --check rc=0,
  openapi_gen_go --check rc=0, go build rc=0, go vet rc=0.
- Not run by this challenger: DB-backed tests, api-gate, e2e-gate (no
  database or Docker on this host).
