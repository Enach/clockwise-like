# Domain notes: auth

Companion to `auth.yaml`. 16 operations documented.

Sources read: `backend/api/routes.go`, `middleware.go`, `handlers_auth.go`,
`handlers_sso.go`, `handlers_me.go`, `handlers_health.go`,
`integration_availability.go`, `handlers_microsoft_auth.go`,
`handlers_settings.go` (for `writeError`), `backend/auth/{jwt,sso_detect}.go`,
`backend/storage/{users,orgs,sso_providers,settings}.go`, and the tests
`handlers_auth_test.go`, `health_test.go`, `integration_availability_test.go`,
`backend/auth/sso_detect_test.go`.

Frontend cross-checked: `src/api/{types,contract,client}.ts`, `src/lib/api.ts`,
`src/contexts/AuthContext.tsx`, `src/components/auth/AuthDialog.tsx`,
`src/api/schedulingLinks.ts`, `src/api/integrationAvailability.test.ts`.

---

## (a) Backend / frontend contract mismatches

1. **`/api/auth/detect` — the SSO branch is dead in the frontend.**
   Backend `auth.DetectResult` returns `{type, provider_name?, redirect_url?}`.
   The frontend declares `interface DetectResponse { type; domain?: string }`
   (`AuthDialog.tsx:32`) and gates the SSO redirect on
   `if (res.type === "sso" && res.domain)` (`AuthDialog.tsx:111`). The backend
   never sends `domain`, so an SSO-configured user falls through to the
   "generic" password step and can never reach `/api/auth/sso/{domain}`.
   The backend *does* hand back `redirect_url` (`/api/auth/sso/<domain>`),
   which the frontend ignores. Either side can be fixed; they must agree.

2. **`login_hint` is sent but never honoured.**
   `AuthDialog.tsx` navigates to
   `/api/auth/google?login_hint=<email>` and `/api/auth/microsoft?login_hint=<email>`.
   `authHandlers.startOAuth` and `startMicrosoftOAuth` read no query params and
   build the provider URL from `state` alone, so the hint is dropped and the
   user still sees an account chooser.

3. **`/api/auth/me` avatar field name.**
   Backend `storage.User` serializes `avatarUrl`. The frontend types the same
   response as `AuthUser { id; email; name; avatar?: string }`
   (`AuthContext.tsx:16`) and stores the raw object — so `user.avatar` is always
   `undefined`. `schedulingLinks.ts` types it more narrowly still as
   `BackendUser = { id; email; name? }`.

4. **`POST /api/auth/logout` returns 200 with an empty body and no
   `Content-Type`.** `src/lib/api.ts` `apiFetch` tolerates this (non-JSON
   content type → `undefined`), but `src/api/client.ts` `requestApi` would
   throw `ApiUnreachableError("Non-JSON response")` for the same shape. Two
   different fetch wrappers with different tolerance are in play; only the
   lenient one currently calls logout. A 204 would be correct and safe for both.

5. **`AuthStatus.provider` optionality.** Frontend types it
   `provider?: CalendarProvider`; the backend always emits it (no `omitempty`)
   and emits `"google"` on *every* failure path. A consumer cannot use
   `provider` to tell "configured as Google" from "we could not read settings".

6. **`/api/auth/status` is not user-scoped.** It branches on the **global**
   settings row (`storage.GetSettings(h.db)` takes no user id); only the google
   branch consults the authenticated user. The frontend treats it as
   per-session state. In a multi-user deployment one user's Outlook/webcal
   configuration is reported to everybody.

7. **No frontend consumer for `/api/admin/sso` (all three methods).** Nothing in
   `smart-calendar-flow/src` calls it, so the wire shape is uncontested — which
   is how the issue in (b)(1) has gone unnoticed.

---

## (b) Surprising / inconsistent things

1. **`storage.SSOProvider` has no json tags.** `POST` and `GET /api/admin/sso`
   therefore return PascalCase Go field names (`ID`, `ProviderName`, …) — the
   only PascalCase payload in this domain — and include
   **`OIDCClientSecret` and `SAMLCert` in plaintext** to any authenticated org
   member. Everything else in the codebase uses snake_case or camelCase tags.

2. **Two error encodings side by side.** `writeError()`
   (`handlers_settings.go:206`) emits `application/json` `{"error": "..."}`.
   Many handlers instead use Go's `http.Error`, which forces
   `Content-Type: text/plain; charset=utf-8`. `ssoHandlers.oidcCallback` uses
   *both*: JSON for its 404, text/plain for its 400s and 500s.

3. **`requireAuth`'s 401 is JSON text served as text/plain.** It sets
   `Content-Type: application/json` and then calls
   `http.Error(w, '{"error":"unauthorized"}', 401)` — `http.Error` overwrites the
   header. Same pattern in `handlers_me.go` for its own 401 and 404. Any client
   that dispatches on Content-Type will not parse these.

4. **Cookie beats bearer.** `requireAuth` reads the `auth_token` cookie *first*
   and only falls back to `Authorization: Bearer`. A stale cookie therefore
   shadows a valid bearer token. The frontend only ever uses the cookie
   (`credentials: "include"`, no Authorization header anywhere).

5. **`issueJWT` fails silently.** If `jwtSecret == ""` or token generation
   errors, it returns without setting a cookie, and every callback still issues
   its `302` to `/auth/callback`. The user lands on the success page logged out.

6. **The Microsoft callback logs in only best-effort.** If the Graph profile
   fetch or user upsert fails, it skips `issueJWT` but still redirects to
   `?provider=outlook` — indistinguishable from success. It also saves the
   Microsoft token on the global settings row, not per user.

7. **`DELETE /api/auth/disconnect` also logs you out.** It clears the
   `auth_token` cookie in addition to deleting the calendar token; the name and
   the frontend's `authDisconnect(): Promise<void>` do not suggest a session
   change.

8. **Duplicate-ish redirect contracts.** Three callbacks redirect to
   `<FRONTEND_URL>/auth/callback`: Google with no query string, Microsoft with
   `?provider=outlook`, OIDC with `?provider=sso`. The SPA must handle the
   absent-parameter case as "google".

9. **`provider_type: "saml"` is storable but unusable.** `POST /api/admin/sso`
   accepts and persists it; `GET /api/auth/sso/{domain}` then returns
   **501 "SAML not yet supported"**. 501 is otherwise unused in the codebase.

10. **`Enabled` is hardcoded `true`** on upsert and there is no update path that
    can set it false — the column exists but is write-once via this API.

11. **`DELETE /api/admin/sso/{domain}` returns 204 for a domain that has no
    provider row** (no rows-affected check), so delete is not observably
    idempotent-vs-missing.

12. **Detect rate limiter is per-process, in-memory and unbounded.**
    `detectLimiter` is a `map[string]*ipBucket` that is never swept, and it keys
    on a client-supplied `X-Real-IP` / `X-Forwarded-For` when present. Documented
    as 429 in the spec; behind multiple replicas the effective limit is
    20/min × replicas.

13. **No Go test coverage for `/api/auth/detect`, `/api/auth/sso/{domain}`,
    `/api/auth/callback/oidc/{domain}`, `/api/auth/me`, `/api/auth/logout`, or
    any `/api/admin/sso` route.** `handlers_auth_test.go` covers only status
    (200), disconnect (204), startOAuth (302 + Location non-empty) and callback
    invalid-state (400). Every other status code in `auth.yaml` is read off the
    handler source, not off a passing assertion.

14. **`GET /api/health` version is the literal `"0.1.0"`** in the handler — not
    injected at build time, so it will not change between releases.

---

## (c) x-uncertain items and what a human should check

| Where | `x-uncertain` | What to verify |
|---|---|---|
| `CurrentUserResponse.provider` | Observed writers pass `"google"`, `"microsoft"`, `"sso"`, but the column is free text with no CHECK constraint found. | Grep the migrations for a constraint on `users.provider`, and confirm no other code path writes a fourth value (e.g. a seed script or the Zoom flow). If it is closed, turn it into an enum. |
| `SsoProviderCreateRequest` (schema level) | Nothing beyond `domain` / `provider_name` / `provider_type` is validated, so an `oidc` row can be created with an empty issuer or client id; the failure only appears later as a 500 from `/api/auth/sso/{domain}`. | Decide whether `oidc_issuer` + `oidc_client_id` + `oidc_client_secret` should be required when `provider_type == "oidc"` (and the SAML trio when `"saml"`), then encode it as a `oneOf` / `dependentRequired`. |
| `SsoProviderResponse` (schema level) | No frontend consumer exists, so the PascalCase key casing is inferred from the absence of json tags rather than observed on the wire. | Hit `GET /api/admin/sso` against a running server and confirm the casing — and confirm whether returning `OIDCClientSecret` / `SAMLCert` is intended before this is published as the contract. |

Additional points a human should settle that are **not** marked `x-uncertain`
because the code is unambiguous, but where the code may not be the intent:

- Whether the global `security` for the assembled document should be
  `bearerAuth` alone. The spec fragment defines both `bearerAuth` and
  `cookieAuth`; protected operations carry no `security:` key and so inherit
  whatever the root document declares. Given the middleware checks the cookie
  first and the frontend only ever sends a cookie, the root document should
  most likely declare `security: [{bearerAuth: []}, {cookieAuth: []}]`.
- `503` on the Microsoft routes is JSON while the neighbouring `400`/`500` on
  the same handler are text/plain. Pick one before clients start branching.
