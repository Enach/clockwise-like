# NEW-auth-contract-prose: auth contract prose after PAC-39 (#215)

Part of PAC-39. Contract revision 1. Bundle sha256
`7cc6b2402984c53d045720e98f5088f2a64fad5ef22c9137802e2e7a7a989bd2`.

## 0. Why there is no spec

PAC-39 (#215) changed two server behaviours with no spec stage and stated them
in its PR body instead. It shipped as "backend only, no contract change", which
was true for shapes but left the contract's prose describing the old behaviour.
Factory §7 rule 2 ("the contract describes what is") makes that a defect. This
change is documentation only: there is no new behaviour to specify, so the
source of truth is the merged code, quoted below. A challenger may still treat
the missing spec for #215 as a finding against PAC-39.

## 1. What the contract said, and what is true

| Place | Said | True since #215 (code) |
|---|---|---|
| `auth.yaml` header comment, `bearerAuth`, `cookieAuth` | the `auth_token` cookie is read first; Bearer only when the cookie is absent | `requireAuth` (`backend/api/middleware.go`): if `Authorization` has the `Bearer ` prefix, that token is the only credential; invalid -> 401, no cookie fallback. Otherwise (no header, or another scheme) the cookie is used. |
| `POST /api/auth/detect` description | sliding window, keyed by `X-Real-IP` / first `X-Forwarded-For` / `RemoteAddr` | `clientIP` (`backend/api/ratelimit_detect.go`): key is the socket peer IP (`RemoteAddr` without port). `X-Real-IP` only when the peer is in `TRUSTED_PROXIES` (empty default). `X-Forwarded-For` never read. Fixed one-minute window, expired buckets swept. |
| `429` on detect | "20 detects/minute from this IP" | 20 in the current one-minute window per key (peer IP, or `X-Real-IP` from a trusted proxy) |
| `auth.notes.md` items 4, 12 and the security-scheme bullet | cookie first; limiter keyed on client headers, never swept | updated to record the #215 fix and keep API-062's open part (per-process limit = 20/min x replicas) |

The ticket's acceptance says "RemoteAddr-only limiter key". The code is not
strictly RemoteAddr-only: a trusted proxy's `X-Real-IP` is honoured. The contract
describes the code, including that exception, because writing "RemoteAddr only"
would be the kind of lie §7 forbids. With the default (empty) `TRUSTED_PROXIES`
the key is RemoteAddr only.

The old text also said "sliding window"; the limiter has always been a fixed
window (`windowEnd` reset on expiry). Corrected in passing because the sentence
was being rewritten anyway; it changes no shape.

## 2. Shape of the change

Description strings only. No operation, schema, status code, `required` or
`security` key changes. Consequences, checked by the gates:

- `backend/api/gen` is unchanged (`OK: backend/api/gen matches the contract`).
- The web's `src/api/generated/types.ts` and `schemas.types.d.ts` change only in
  JSDoc `@description` comments (web branch
  `factory/new-auth-contract-prose-types`).
- No consumer in either repo reads these descriptions at runtime.

## 3. Alternatives rejected

1. **Make the precedence machine-readable by changing the global `security`
   to `[{bearerAuth: []}, {cookieAuth: []}]`.** OpenAPI security alternatives
   express "either is accepted", not an order, so it could not encode
   Bearer-over-cookie anyway, and it would change the generated server
   interface and the web types. That is a contract change with consumers, out
   of scope for a prose correction; it stays an open point in `auth.notes.md`.
2. **Add an `x-rate-limit` extension or a `RateLimit` response header to the
   429.** The server sends no such header; specifying one would describe what
   should be, not what is.
3. **Write "RemoteAddr only" as the ticket phrased it.** Rejected in section 1:
   the trusted-proxy path exists and production enables it.
4. **Leave `auth.notes.md` alone because it is not assembled.** Rejected: it is
   the fragment's reviewer-facing companion and was stating the old behaviour
   as current.

## 4. Findings and uncertainties

- API-008: already *Fixed (PAC-39)* in `docs/factory/api-audit.md`; the contract
  now agrees with it.
- API-062: *Partly fixed (PAC-39)*; the contract keeps the per-process caveat.
- No `x-uncertain` marker sits on `detectAuthProvider` or on the security
  schemes; none resolved, none added.

## 5. Decisions

None needed: no product choice is made here.

## 6. Rollback

Revert the contract PR and the types PR. No runtime effect.

## Challenge — 2026-10-26

Reviewer: contract-challenger (separate agent; read the committed artefacts on
`factory/new-auth-contract-prose-contract` at `ba052b2`, the code at that
commit, and the web types branch; not the author's reasoning).

**Verdict**: accept-with-changes

The assembled contract (the description strings that reach the bundle and the
generated types) matches the code. The one blocking finding is in the
reviewer-facing prose this change edits: it rejects an alternative, and keeps an
open point, on the premise that the root `security` is `bearerAuth` alone,
which the bundle contradicts.

### Requests wrongly permitted

None. No schema, `required`, status code or `security` key changed, and the
bundle/gen check confirms it. Requests attacked against the new prose:

1. `GET /api/auth/me` with `Cookie: auth_token=<valid JWT for user A>` and
   `Authorization: Bearer <valid JWT for user B>` — contract says Bearer wins;
   `requireAuth` (`backend/api/middleware.go:32-33`) takes the header token, so
   the request runs as B. Pinned by
   `TestRequireAuth_ValidCookieAndValidBearer_BearerIdentityWins`.
2. Same with `Authorization: Bearer garbage` and a valid cookie — contract says
   401, no fallback; code sets `tokenStr = "garbage"`, `ValidateToken` fails,
   401 at `middleware.go:43-46`. Pinned by
   `TestRequireAuth_InvalidBearerAndValidCookie_Rejected`.
3. `POST /api/auth/detect` 21 times in a minute from one socket peer, each with
   a different `X-Forwarded-For: 203.0.113.N` — contract says XFF is never read;
   `clientIP` (`backend/api/ratelimit_detect.go:96-114`) never reads it; the 21st
   is a 429 (`handlers_sso.go:24-27`). Pinned by
   `TestDetectLimiter_RotatingXForwardedFor_StillThrottled`.
4. Same with rotating `X-Real-IP` from an untrusted peer — ignored, throttled
   (`TestDetectLimiter_RotatingXRealIPFromUntrustedPeer_StillThrottled`); from a
   trusted peer with `X-Real-IP: not-an-ip` — `net.ParseIP` is nil, keys on the
   peer, as the description's "holds a valid IP" says.

### Responses the client cannot handle

None. The web types branch `factory/new-auth-contract-prose-types` changes
`src/api/generated/types.ts` and `schemas.types.d.ts` in JSDoc lines only
(checked: every non-comment `+`/`-` line of `git diff origin/main...` is a
`/** @description */` line). No consumer reads descriptions at runtime.

### Consumers affected

- None at runtime. `mcp/` has its own `bearerAuth` (static token, `mcp/auth_test.go`)
  and calls the backend server-side; it never sends the `auth_token` cookie, so
  precedence cannot change its behaviour.
- Non-blocking, stale prose outside this change's `allowedPaths` (left for a
  follow-up, not a reason to widen this PR):
  - `e2e/auth.ts:5-7` still lists "1. the `auth_token` cookie 2. an
    `Authorization: Bearer` header" as the order `requireAuth` uses.
  - `docs/factory/api-audit.md:434` still says "`requireAuth` checks the cookie
    first" in the finding narrative (the table row at :266 is already marked
    Fixed).

### Contract/implementation divergences

1. **Blocking.** The root document already declares
   `security: [{bearerAuth: []}, {cookieAuth: []}]` (`contracts/openapi/openapi.yaml:24-26`,
   present on `origin/main` since #173). Three statements in this change
   contradict that:
   - `.md` §3 alternative 1 rejects "changing the global `security` to
     `[{bearerAuth: []}, {cookieAuth: []}]`" because it "would change the
     generated server interface and the web types" and "stays an open point".
     It is already the state, so the alternative was rejected for a reason that
     is not real.
   - `auth.notes.md:157-162` (edited by this change) still says the root
     "should most likely declare" that same value — an open point that is
     already closed.
   - `auth.yaml:12-13` (in the header paragraph this change rewrote) says
     protected operations "inherit the global scheme (`bearerAuth`)".
   Fix (prose only, no regen impact): in `.md` §3 replace alternative 1 with the
   real constraint — the root already lists both schemes as alternatives, and
   OpenAPI alternatives cannot express precedence, so precedence lives in the
   scheme descriptions; in `auth.notes.md` close that open-point bullet as
   "already the root value since #173"; in `auth.yaml:13` say "inherit the
   global requirement (`bearerAuth` OR `cookieAuth`)".

2. Non-blocking. `POST /api/auth/detect` description says `TRUSTED_PROXIES` is
   "empty by default, so no proxy is trusted". True of the binary
   (`os.Getenv`, `ratelimit_detect.go:33`) and of `docker-compose.yml:31` and
   `.env.example:18`, but `docker-compose.prod.yml:47` defaults it to
   `172.16.0.0/12,192.168.0.0/16,10.0.0.0/8` (safe there: the backend port is
   unpublished and `nginx/nginx.conf:8,15` overwrites `X-Real-IP` with
   `$remote_addr`). Suggest "empty unless the operator sets it; the production
   compose file trusts the Docker private pools", so a reader of the contract
   does not conclude production keys every user behind nginx on one bucket.
   `.md` §3 alternative 3 already says production enables it, so the two
   artefacts currently disagree in emphasis.

3. Non-blocking edge of "a `Bearer ` header wins". Go trims trailing whitespace
   from header values, so `Authorization: Bearer ` (empty token) arrives as
   `Bearer`, fails `HasPrefix(h, "Bearer ")` and falls back to the cookie;
   `Authorization: bearer <jwt>` (lower case, valid per RFC 7235's
   case-insensitive scheme) also falls back to the cookie. The header comment's
   "(none, or another scheme)" covers neither literally. Suggest "a header value
   starting with the case-sensitive `Bearer ` followed by a non-empty token".

### Uncertainties not actually resolved

None claimed: `resolvesUncertain` is empty and no `x-uncertain` sits on
`detectAuthProvider` or the security schemes. API-008/API-062 are prose
updates; the four `behaviourTestsAlreadyInTree` exist
(`backend/api/middleware_test.go:37,54,71`, `ratelimit_detect_test.go:47`) and
passed in the gate run below.

### Verified clean

- Fixed window (not sliding): `windowEnd` set once per bucket, reset only on
  expiry (`ratelimit_detect.go:55-58`). Sweep "at most once a minute": guarded
  by `lastDetectSweep` (`:44-52`).
- 429 body: `writeError(w, "rate limit exceeded", 429)` — JSON
  `ErrorResponse`, as the unchanged response schema says.
- Acceptance tests vs spec: there is no spec (§0, accepted as stated); the
  three manifest acceptance items are each checkable and were checked above.
- Gate, run by this reviewer on `ba052b2`:
  `bash .claude/factory/api-gate.sh factory/new-auth-contract-prose-contract` →
  `make openapi rc=0`, `regenerated: none`, `OK: bundle is in sync (98 paths)`,
  `OK: backend/api/gen matches the contract`, `openapi-check(api) rc=0`,
  backend/mcp lint rc=0, backend/mcp test rc=0, coverage backend 53.2% (floor
  43%) rc=0, mcp 84.0% (floor 75%) rc=0. `web-gate.sh` was not run by this
  reviewer.
