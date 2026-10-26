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
