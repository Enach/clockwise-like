# Contract — PAC-97: sign-in detection follows the domain's mail, `choose` when unsure

- **Spec**: `docs/specs/PAC-97.md` (accepted with changes by spec-challenger, 2026-11-13)
- **Manifest**: `contracts/features/PAC-97.yaml`
- **Revision**: 1
- **Fragment changed**: `contracts/openapi/paths/auth.yaml` (`detectAuthProvider` description, `SsoDetectResponse.type`)
- **contractHash**: `cc48d6908a8fa9cc6712b0be43ebd1748253f4d99fbe17172ad01bef5af51e87` (main `bc7b144` was `d02f9be1…40f`)

## 1. What changes at the HTTP boundary

One value and two descriptions. Nothing else.

| Item | Before | After |
|---|---|---|
| `SsoDetectResponse.type` enum | `google, microsoft, sso, generic` | `google, microsoft, sso, generic, choose` |
| `SsoDetectResponse.type` description | "microsoft from a live tenant probe … else google" | the ordered rule: generic (consumer/unparseable only) → sso → MX strong match → `choose` |
| `detectAuthProvider` description | rate limit only | + "a failed or slow lookup is a 200 `choose`, never an error; only a failed SSO-row read is a 500" |
| Statuses | 200, 400, 429, 500 | unchanged |
| Request | `{email}` | unchanged |
| `provider_name`, `redirect_url` | only for `sso` | unchanged (absent for `choose`) |

`choose` carries no other field. The response keeps
`additionalProperties: false`, so AC-16's strict decode proves that.

### 1.1 The rule the description pins

The description is the normative text; the spec's D-2/D-3/D-7/D-8 are the
source.

1. No domain, or a domain on the fixed consumer list → `generic`.
2. Active SSO row for the exact domain → `sso` (as today).
3. MX lookup for the fully-qualified domain (one query, 2 s budget, the
   server's resolver, only the domain). Take the records with the lowest
   preference. Answer P when every one of them matches P and no record at any
   preference matches the other provider.
   - Google: `google.com`, `googlemail.com`, or ending in `.google.com` /
     `.googlemail.com`.
   - Microsoft: ending in `.mail.protection.outlook.com`,
     `.mail.eo.outlook.com` or `.mx.microsoft`.
   - Lower-cased, one trailing dot removed, label-boundary match.
4. Everything else → `choose`, including every failure.

Live public MX answers (spec Challenge log, C-1) agree with this rule for
gorgias.com, shopify.com, stripe.com (google), google.com (`smtp.google.com`,
google), microsoft.com, github.com, unilever.com (microsoft), and
accenture.com, proofpoint.com, mimecast.com (gateway, `choose`).

`.mx.microsoft` is Microsoft's MX form for Exchange Online with inbound
DNSSEC/DANE (`<MX token>.<subdomain>.mx.microsoft`, Microsoft Learn "How SMTP
DANE works", updated 2026-07-21; the zone is delegated to Azure DNS). The same
page names `mail.protection.outlook.com` and `mail.eo.outlook.com` as the
legacy global names. Deliberately **not** Microsoft:
`*.olc.protection.outlook.com` (Outlook.com consumer custom domains, i.e.
personal accounts, refused until PAC-77) and the sovereign clouds
(`office365.us`, `partner.outlook.cn`, `outlook.de`), because Paceday signs in
only through the global endpoints.

## 2. Server behaviour behind the contract (not HTTP-visible)

- No third-party HTTP call from detection; the openid-configuration probe
  and its unbounded cache are removed. Nothing else reads them
  (`isMicrosoftTenant`/`tenantCache` have no other caller; PAC-71 takes the
  tenant from the signed ID token).
- Cache per process: definite DNS answers 1 h, failures 1 min, 10 000 domains,
  oldest evicted; one shared lookup per domain under its own budget; a
  caller's cancellation is never cached.
- Invalid, single-label, non-ASCII or over-long names are never queried.
- The rate limiter (20/min per client IP, per process, API-062) is untouched.

## 3. Web client (paired PR)

1. `google` / `microsoft` / `sso` / `generic`: as today.
2. `choose` **or any value the web does not know**: a choose step with the
   available provider buttons ("Continue with …" / "Sign up with …"), each
   navigating to `/api/auth/<provider>?login_hint=<email>`; a back action that
   keeps the email; if no provider is available, a message and back. Never
   the password step.
3. The typed email is kept in `sessionStorage` only, cleared on a completed
   sign-in and on sign-out. After `?error=auth_failed` or
   `?error=signin_refused`, the notice's retry offers both available
   providers with it as `login_hint`. Storage failures degrade to today's
   empty dialog.
4. Regenerated `src/api/generated` gains `choose` in the union and the zod
   enum. `apiFetch` does not parse this answer with zod, so no runtime
   validation changes.

## 4. Compatibility and order

| Web | Backend | Result |
|---|---|---|
| old | old | today |
| new | old | today (never sees `choose`) |
| new | new | the fix |
| old | new | `choose` → password step: the dead end of spec B-1 for gateway/unknown domains |

Hence: **deploy the web first, then merge the backend**. Residual: a tab that
loaded the old web before the web deploy and is still open after the backend
deploy, until it reloads. Rollback is the reverse: backend first.

## 5. Alternatives rejected

1. **Change the meaning of `generic` instead of adding a value.** The old web
   would show the password step for both meanings, and the new web would have
   no way to tell a consumer address from a company domain whose mail host is
   unknown. Two meanings in one value is the bug class this ticket fixes.
2. **A client opt-in on the request (`{"email", "accepts": ["choose"]}`)
   so the server answers `google` to old clients.** It would remove the
   deploy order. Rejected: the opt-in is protocol surface the server must
   honour forever for a window bounded by tab lifetime, and the fallback it
   buys (`google` for every unknown domain) is today's misroute, which is
   worse than a visible dead end that a reload fixes. The request schema's
   `additionalProperties: true` would let it be added later without breaking
   anyone if the window ever matters.
3. **Return the MX evidence (`mail_host: "google" | "microsoft" | "other"`,
   `confidence`) and let the client decide.** It moves the routing rule into
   every client and exposes server lookup details for no user benefit. One
   enum value states the decision.
4. **Keep the tenant probe as a tie-breaker for gateway domains, or use
   getuserrealm.** Rejected in spec D-5: an Entra tenant is exactly the weak
   signal that misroutes gorgias.com; getuserrealm needs a login name and is
   undocumented. A gateway domain costs one click on the choose step.
5. **A new status (e.g. 300 or 409) for "ambiguous".** Ambiguity is a normal
   answer, not an error; the old web would treat a non-2xx as a failure and
   show an error instead of the password step, and every client would need
   a second parsing path.

## 6. Findings and uncertainties

- API-010: stale audit row; the implementer marks it "Fixed (PAC-35)".
- API-092: `choose` gets a router-level Go assertion (AC-16).
- API-062: unchanged and still stated in the operation description.
- No `x-uncertain` marker on the operation or schema; none added.
- Not resolved, filed separately: spec B-1 (the web's password step posts to
  `/api/auth/login` and `/api/auth/register`, which do not exist).

## 7. Merge interaction with PAC-87

PAC-87 (`impl/pac-87-api`, contractHash recorded on its branch) changes
`habits.yaml` and `scheduling.yaml`, not `auth.yaml`, but shares the bundle and
`backend/api/gen/paceday.gen.go`. PAC-87 is further along and is expected to
merge first; **PAC-97 merges second**, re-runs `make openapi` in an LF clone on
top of PAC-87, commits the regenerated bundle and gen, and records the new
contractHash in `PAC-97.yaml` (fragment text unchanged). If the order flips,
PAC-87 does the same.

## 8. Gate output (LF clone of `factory/pac-97-c` at `d4b32d8`, 2026-11-13)

```
make openapi rc=0
  operations : 130  (12 generated, 118 handwritten)
regenerated: backend/api/gen/paceday.gen.go contracts/openapi/openapi.yaml
OK: bundle is in sync (108 paths)
OK: MIGRATION.md covers all 130 operations (12 generated)
OK: backend/api/gen matches the contract
SKIPPED: web generated types (no repo at /nonexistent) — factory §2 gate 4 is only half checked.
openapi-check(api) rc=0
build-ok
cc48d6908a8fa9cc6712b0be43ebd1748253f4d99fbe17172ad01bef5af51e87  contracts/openapi/openapi.yaml
```
