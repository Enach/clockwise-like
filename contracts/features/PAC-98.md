# Contract — PAC-98: no password sign-in; personal addresses go to a provider

- **Spec**: `docs/specs/PAC-98.md` (accepted with changes by spec-challenger, 2026-11-13; owner questions OQ-1..OQ-4 open, factory defaults apply)
- **Manifest**: `contracts/features/PAC-98.yaml`
- **Revision**: 1
- **Fragment changed**: `contracts/openapi/paths/auth.yaml` (`detectAuthProvider` description, `SsoDetectResponse.type` description)
- **contractHash**: `3221112e60941b8d3ec78721ba7c8642bcec76a8a61e0bd473ae7da1395c07d7` (main `68540f6` was `5367da00…3328`)

> **Factory-assumed default (spec banner, OQ-1).** Paceday has no password
> sign-in. If the owner chooses to build password sign-in instead, this
> contract revision is withdrawn together with the spec, not amended.

## 1. What changes at the HTTP boundary

Only descriptions change. No value is added or removed, and no status or field changes.

| Item | Before | After |
|---|---|---|
| `SsoDetectResponse.type` enum | `google, microsoft, sso, generic, choose` | unchanged |
| `generic` | no domain, or any consumer-list domain; old clients show a password form | only a consumer-list domain other than gmail.com/googlemail.com: a personal address, so the client offers the providers that accept personal accounts |
| gmail.com, googlemail.com | `generic` | `google` |
| outlook.com, hotmail.com, live.com, msn.com | `generic` | `generic` until PAC-77 (stated in the description) |
| Address with no domain | `generic` | `choose` |
| Password form | implied for `generic` ("choose … never shows a password form") | never, for any value; "Paceday has no password sign-in and no password or registration route" |
| Unavailable provider | not described | a client that has read `getIntegrationAvailability` and finds the answered provider not offered shows the provider choice instead of starting a sign-in |
| `detectAuthProvider` description | MX rule and 500 | + "an address without a domain, or on the consumer list, is answered without any SSO read or lookup" |
| Statuses, request, `provider_name`, `redirect_url` | 200/400/429/500, `{email}`, only for `sso` | unchanged |

No operation is added. `/api/auth/login` and `/api/auth/register` stay
absent, which is the point. The contract now says in words that they do
not exist, so a client author who reads the contract (Lovable included,
`AGENTS.md` Scope) has no gap to fill.

### 1.1 The rule the description pins

0. No domain: no `@`, or nothing after the first `@` → `choose`. No SSO read, no lookup.
1. Consumer list → gmail.com and googlemail.com `google`; every other entry
   `generic`. No SSO read, no lookup, so an SSO row for a consumer domain
   never applies (spec AC-5).
2. Active SSO row → `sso` (unchanged).
3. MX strong match → `google` / `microsoft` (PAC-97, unchanged).
4. Everything else → `choose` (unchanged).

Step 0 is listed first because the server checks it first
(`sso_detect.go:166-169` tests `dom == ""` together with the consumer list).
The answer moves from `generic` to `choose`, because an address without a
domain is not a personal address.

## 2. Server behaviour behind the contract (not HTTP-visible)

- The consumer list (`backend/domain/domain.go:5-14`) is unchanged. Organisation
  matching (`storage/orgs.go:78-89`) and the PAC-71 review-log condition
  (`storage/signin.go:277`) keep reading the same list. AC-21 pins the
  organisation skip, which no test covered before.
- The answer is a server constant per domain. Nothing is cached and nothing
  is logged beyond today.
- The PAC-71 evidence rules and the personal-Microsoft refusal
  (`handlers_pac71_auth.go:81-95,252-256`) are untouched. Detection only
  chooses which start to offer.

## 3. Web client (paired PR)

Normative text is spec §3 and AC-8..AC-17. In summary:

- `generic` → the personal-address step: the address, Google with the hint
  when available ("Sign up with Google" in sign-up mode), a "personal
  Microsoft accounts can't sign in to Paceday yet" sentence with no date, no
  Microsoft button until PAC-77, "Use a different email", and a "not
  available on this deployment" state.
- No value shows a password field, and nothing posts to a password route.
  An `sso` answer without a path goes to the PAC-97 choose step.
- A `google` or `microsoft` answer for a provider that availability (loaded)
  says is not offered → the provider step for that address, with no
  navigation. While availability is loading or failed → navigate, as today.
- Failure return: the remembered email keeps its detection kind
  (`sessionStorage`, PAC-97 D-10). With a kind: zero detection requests.
  Without one: exactly one.
- The web gate fails on `auth/login` / `auth/register` anywhere in `src/`
  (non-test) and on a password input under the sign-in surfaces. It carries
  a self-test (AC-13). `AGENTS.md` and `00-global-context.md` carry the
  PAC-98 rule (AC-17).
- Generated types: regenerated (`REGEN=1` web-gate). Only doc comments
  change; the union is the same.

## 4. Compatibility and order

The enum is unchanged, so each half is safe alone on a Google-on deployment
(spec §7 table, builds W0/W1/W2 × B1/B2). The one cell worse than today is
the new backend with an old web on a **Google-off** deployment. There a
gmail.com address navigates to the Google start, which has no "configured"
check (`handlers_auth.go:26-41`), and lands on Google's error page instead
of the password form. Production offers Google, so no order is required.
Web-first is preferred when it costs nothing.

`generic` read by each build: W0/W1 "password step", W2 "personal-address
step". No build navigates on it, so the meaning change cannot send anyone to
a wrong provider.

## 5. Alternatives rejected

- **A new enum value `personal`, and drop `generic`.** The name would be
  clearer, but older webs treat an unknown value as `choose` (W1) or the
  password step (W0). With `choose`, W1 would offer Microsoft to a personal
  address, which PAC-71 then refuses. It would also force web-first
  deployment and an enum change in the generated types of both repos, for
  no behaviour that `generic` cannot carry (spec D-4).
- **Answer `choose` for personal addresses.** This removes a value, but the
  web then cannot tell a personal address from a company one, which the
  personal-address step and the failure return (AC-15) need. The only other
  way would be for the web to hold its own copy of the consumer list, and
  copies drift.
- **Answer `microsoft` for outlook.com/hotmail.com/live.com/msn.com now.**
  Microsoft sign-in refuses every personal account until PAC-77, with a notice
  that names no cause. Sending people there on purpose builds a second dead
  end (spec D-3, OQ-2).
- **Add password operations (`/api/auth/login`, `/api/auth/register`) to the
  contract**, to make the web's calls valid. That is the owner's alternative
  (OQ-1). It needs credential storage, verification, reset, brute-force
  protection and a PAC-71 binding rule for a password identity. It is a
  separate ticket, not a description edit.
- **Encode "provider unavailable" in the detection answer** (for example
  `google` only when Google is configured). Availability already has its own
  operation (`getIntegrationAvailability`), and the PAC-97 web already reads
  it. Folding it into detection would make the answer depend on deployment
  configuration and change company-domain answers for old webs. The
  contract states the client rule instead.

## 6. Findings and uncertainties

- No `x-uncertain` marker on `detectAuthProvider` or `SsoDetectResponse`;
  none added.
- API-092: the changed outcomes (gmail.com → google, yahoo.com/outlook.com →
  generic, no domain → choose) get Go assertions through the real router
  (spec AC-6).
- API-062 (per-replica detect rate limit): unchanged and still open, as the
  description already says.
- PAC-97's manifest `doesNotResolve` B-1 (the password step) is resolved by
  this ticket.

## 7. Gate output (LF clone of `factory/pac-98-c` at `c98f169`, golang:1.25-bookworm, 2026-11-13)

```
make openapi rc=0            (130 operations, 12 generated; regenerated backend/api/gen/paceday.gen.go, contracts/openapi/openapi.yaml)
OK: bundle is in sync (108 paths)
OK: MIGRATION.md covers all 130 operations (12 generated)
OK: backend/api/gen matches the contract
SKIPPED: web generated types (no repo at /nonexistent)
openapi-check(api) rc=0
cd backend && go build ./... ok
sha256 contracts/openapi/openapi.yaml = 3221112e60941b8d3ec78721ba7c8642bcec76a8a61e0bd473ae7da1395c07d7
```

The regenerated files are committed (`4b11ccb`). The committed blob's sha256
equals the contractHash. Only doc comments changed in
`backend/api/gen/paceday.gen.go`.
