# PAC-45 review — 2026-09-17

Reviewer: `reviewer` agent (stage 6). Branch
`origin/nihochart/pac-45-oidc-email-domain-binding`, commit `b1a07bb`, PR #172.
Read-only review: this environment blocks the Go module proxy, so **nothing was
compiled and no test was executed**. Every claim below is from source.

There is no `docs/specs/PAC-45.md` — `docs/specs/` does not exist on this branch —
so the review is against the diff and the "Minimum fix" section of the issue.

**Verdict**: request-changes (one blocking item, documentation-only; see §1)

---

## Blocking

1. **The new `403` is not in the contract.** `backend/api/handlers_sso.go:182`
   adds a `403 text/plain` response to `ssoOidcCallback`, but
   `contracts/openapi/paths/auth.yaml:285-334` still documents exactly
   `302 / 400 / 404 / 500` for that operation, and the bundle was not
   regenerated. The endpoint is `handwritten` in
   `contracts/openapi/MIGRATION.md:59`, so `make openapi-check` only proves the
   *generated* code matches the bundle — it never compares a hand-written
   handler to the contract — and there is no contract test naming
   `ssoOidcCallback` anywhere under `backend/api/`. The divergence is therefore
   invisible to `make verify` and will stay invisible.

   Failure scenario: a client (or the frontend's generated zod schemas)
   generated from the current bundle sees `403` as an undocumented status and
   falls through to its unknown-error branch; more importantly the register that
   the factory depends on now asserts a response set the server does not have.
   Factory README §2 stage 2 and §7 rule 2 ("the contract describes what is, not
   what should be") both bite here.

   Fix: add the `'403'` response with the literal message
   `"email domain does not match SSO provider domain"` to the `ssoOidcCallback`
   responses in `contracts/openapi/paths/auth.yaml`, using
   `PlainTextError` to match the other non-404 errors on that operation, then
   `make openapi`. No code change, no re-test.

   This is a YAML edit, not a reason to hold a live auth-bypass fix. If the team
   decides to merge first, record the acceptance and the follow-up explicitly in
   the PR per §2 stage 6 rather than letting it drift.

2. **`make verify` output not verifiable from here.** Factory §3 rule 1 requires
   it in the PR body. I have no network access to GitHub (`gh` is not installed,
   egress is blocked), so I could neither confirm it is present nor that it is
   complete. Flagging, not asserting a defect. Note that
   `backend/api/TestMain` (`backend/api/testhelpers_test.go:14-34`) hard-exits
   when the test database cannot be created, so the three new handler tests
   require Docker/testcontainers — they are not covered by a `-short` run.

---

## Non-blocking

1. **The rejection is silent.** `handlers_sso.go:181-184` returns 403 and writes
   nothing anywhere. An IdP asserting an email outside its own domain is the
   highest-signal security event this codebase can observe, and after this fix
   it is detectable and still leaves no record — not a `log.Printf`, not a
   `storage.WriteAuditLog`. One log line at the reject would cost two lines.
   Full audit logging is genuinely blocked on schema: `audit_log` is
   `(action, details, created_at)` only (`backend/storage/focus_blocks.go:61-63`),
   with no user or org column, so it cannot attribute the attempt — which is why
   this is non-blocking and belongs with the wider audit-log work.

2. **Error shape (the `http.Error` / `writeError` split).** The new 403 at
   `handlers_sso.go:182` is `text/plain`; the 404 eight lines above at
   `handlers_sso.go:151` is the JSON envelope. Judged **not blocking**, for three
   reasons: (a) the new line is consistent with every *other* error in
   `oidcCallback` (`:139, :144, :164, :170, :174`) — the 404 is the outlier, and
   the contract already says so in the operation description at
   `contracts/openapi/paths/auth.yaml:294-296`; (b) API-036 as registered in
   `docs/factory/api-audit.md:270` is scoped to `POST /api/schedule/create`, not
   to this operation, so §7's "touch it, fix it" rule does not attach — the
   general envelope cleanup is batch **C2** (`api-audit.md:539-546`), 111 call
   sites, deliberately not this PR; (c) this endpoint is reached by browser
   navigation from the IdP, so the body is rendered as a page, not parsed by the
   SPA. Converting it alone would make the file *less* internally consistent.

3. **The asserted email is not normalised before `UpsertUser`.**
   `EmailBelongsToDomain` lowercases and trims for the *comparison*, but
   `handlers_sso.go:186` passes `userInfo.Email` raw to `UpsertUser`, which keys
   on `ON CONFLICT (email)`. So `"alice@ACME.com"`, `"alice@acme.com "` and
   `"alice@aKme.com"` (U+212A KELVIN SIGN, which `strings.ToLower` folds to `k`)
   all pass the check for `d = "acme.com"` / `"akme.com"` and each creates a
   *separate* user row. This is **not** a bypass — taking over an existing
   account needs the byte-exact stored email, which in turn needs `d` to be that
   account's real domain — but it lets a holder of one domain accumulate
   look-alike accounts inside it. Normalising the email once at the top of the
   callback would close it.

4. **Trailing-dot and IDN forms fail closed, not open.** `alice@acme.com.` and a
   U-label asserted against a punycode-stored provider row both return 403. That
   is the right direction, but if a real IdP ever emits either form the operator
   gets an opaque 403. Worth a line in the SSO docs when SSO is first enabled.

5. **Test isolation across the global OIDC provider cache.**
   `auth.NewOIDCClient` caches `*gooidc.Provider` by issuer URL forever
   (`backend/auth/oidc.go:25-48`) and nothing resets it between tests. Each
   `newFakeIdP` gets a fresh `httptest.Server` with a fresh RSA key but the
   issuer is `http://127.0.0.1:<port>`; if the OS reuses a port within one `go
   test` process, test *n+1* reuses test *n*'s cached provider and its
   `RemoteKeySet`, whose cached key has the same `kid` ("test") and a different
   modulus. go-oidc re-fetches on a verification miss, so this should recover,
   but it is a latent flake and I could not run the suite to say. A per-IdP
   `kid` would remove the ambiguity cheaply.

6. **Pre-existing, untouched, and right next door:** the doc comment for
   `DeriveOrgName` in `backend/domain/domain.go:41-44` sits above
   `DomainMatchesOrg`, and `DomainMatchesOrg`'s own comment is stranded inside
   it. On `main` already; the new function was inserted cleanly above it and did
   not make it worse. Mentioning only so the next reader does not attribute it
   to this PR.

---

## Tests that do not test what they claim

1. **`TestEmailBelongsToDomain`, `backend/domain/domain_test.go:58-83`** — the
   two cases at `:73-74`, commented "Smuggling a second address past the domain
   extraction", are green with the `strings.Count(email, "@") != 1` guard at
   `backend/domain/domain.go:34-36` **deleted**. `ExtractDomain` uses
   `SplitN(email, "@", 2)`, so `"victim@example.com@acme.com"` extracts
   `"example.com@acme.com"` and `"victim@acme.com@example.com"` extracts
   `"acme.com@example.com"`; neither equals `"acme.com"`, so both return false
   either way. I walked all eleven cases: **every one of them passes with the
   guard removed** — `{"acme.com", …}`, `{"", …}` and `{"alice@", ""}` are all
   caught by `emailDomain != ""` instead. The guard is currently untested.

   A case that does discriminate: `{"alice@x@acme.com", "x@acme.com", false}` —
   false with the guard, **true** without it. Add it, or drop the guard's
   pretence in the comment.

2. **No test asserts the 403 body or `Content-Type`.** All three handler tests
   check only `rec.Code`. Swapping `http.Error` for `writeError`, or changing the
   message to anything at all, leaves the suite green. That cuts both ways — it
   means non-blocking item 2 above can be resolved later without touching tests,
   but it also means the wire shape of the new error is pinned by nothing.

3. **No test that the reject path creates nothing.**
   `TestOIDCCallback_RejectsEmailOutsideProviderDomain` proves an *existing*
   victim row is not overwritten, which is the important half. Nothing proves
   that a cross-domain assertion for an email with no existing row does not
   create one — i.e. that the 403 precedes `UpsertUser` in general rather than
   just failing to clobber.

Everything else about these tests is load-bearing, see Verified.

---

## Verified

- **The hole is closed.** I re-ran the issue's chain against the post-fix source.
  Attacker signs up via Google (`handlers_auth.go:41-79`) as `mallory@evil.com`;
  `AssociateUserWithOrg` (`storage/orgs.go:42-52`) gives them `OrgID` for
  `evil.com`; `POST /api/admin/sso` passes its only gate
  (`handlers_sso.go:203`, `:238`) for `body.Domain = "evil.com"`; they point
  `oidc_issuer` at an IdP they run; they set the `sso_state` cookie themselves
  (they control their own cookies, so `handlers_sso.go:142-146` is not a
  barrier) and drive `/api/auth/callback/oidc/evil.com`; their IdP signs an
  `id_token` with `email = "ceo@victimcorp.com"`, which `Exchange`
  (`auth/oidc.go:66-88`) verifies correctly because it *is* correctly signed by
  the issuer in that provider row. The chain now dies at
  `handlers_sso.go:181`: `EmailBelongsToDomain("ceo@victimcorp.com", "evil.com")`
  is false, 403, return — **before** `UpsertUser` at `:186`, so no row is
  touched, and before `issueJWT` at `:193`, so no cookie is minted. The check is
  in the only place a session is minted from an IdP assertion; `startSSO` mints
  nothing and SAML is a 501 (`handlers_sso.go:99-102`).
- **I could not break `EmailBelongsToDomain`** (`backend/domain/domain.go:33-40`).
  Attempts: case on both sides (`ExtractDomain` lowercases and trims the domain
  half; `d` is lowercased and trimmed in the function — symmetric); zero `@`
  (`"acme.com"` → `ExtractDomain` returns `""` → the `emailDomain != ""` guard);
  two `@` in either order (false with *or* without the count guard, see above);
  empty email; empty `d` (`"alice@"` against `""` → `emailDomain == ""` →
  false — the guard is load-bearing here and is tested); leading/trailing
  whitespace on either side; trailing dot (fails closed); a quoted local part
  containing `@` (fails closed, RFC-exotic); Cyrillic and other homographs
  (distinct byte strings, so they need their own provider row); Unicode
  case-folding oddities (KELVIN SIGN etc. — they widen the set of strings that
  *match*, but see non-blocking 3: they can only create new rows, never collide
  with an existing account, because collision needs the byte-exact stored
  email). Every malformed input I constructed fails closed. No email that should
  pass, fails.
- **Exact match is the right call, and it locks nobody out.** I traced every way
  a user reaches `oidcCallback`. `DetectAuthProvider`
  (`auth/sso_detect.go:25-56`) derives `d` with `ExtractDomain(email)` — the
  user's exact, lowercased email domain — looks the provider up with
  `GetSSOProviderByDomain`, whose SQL is `WHERE domain = $1 AND enabled = true`
  (`storage/sso_providers.go:47-54`), and hands back
  `redirect_url = "/api/auth/sso/<that same exact domain>"`. `startSSO`
  (`handlers_sso.go:92-130`) then puts that same `d` in the `sso_state` cookie
  and in the callback URL, and the callback re-reads it from the path. So on the
  only routed flow, `d` *is* the user's email domain by construction. A user at
  `eu.acme.com` is never routed to an `acme.com` provider row today — detect
  simply finds no row and falls through to Microsoft/Google — so making the
  callback a suffix match would not rescue anyone; it would only re-open the
  hole for subdomains. Conversely, `ListSSOProvidersByOrg` joins
  `organizations o ON o.domain = s.domain` (`storage/sso_providers.go:88-90`)
  and `AssociateUserWithOrg` keys on the exact domain, so exact is what the rest
  of the system already means by "matches". **There is no legitimate flow with a
  mismatch.** The one real behaviour change is an IdP that asserts guest or
  contractor addresses from outside the domain it is configured for — those
  users would now get a 403. On a deployment that has never used SSO that is
  nobody, and it is the correct default; it is worth a release note if SSO is
  ever switched on for a tenant with an IdP like that.
- **The three handler tests are real, not mock theatre.**
  `backend/api/handlers_sso_test.go` stands up an actual `httptest` OIDC server
  with a real JWKS and an RS256-signed `id_token` (`:29-96`), writes a real
  provider row through `storage.UpsertSSOProvider`, and drives the real
  `oidcCallback` through a real chi router against the shared test database
  (`openTestDB` returns the process-wide DB from `TestMain`, so the victim
  seeded at `:140` is in the same database the handler reads — that assertion is
  not vacuous). I checked what change would leave each green: replacing the
  exact match with `DomainMatchesOrg` fails
  `TestOIDCCallback_RejectsSubdomainEmail` (`:162`); swapping the argument order
  fails `TestOIDCCallback_AcceptsEmailInProviderDomain` (`:172`, and that test
  also pins case-insensitivity via `"Alice@PAC45-legit.test"`); moving the check
  after `UpsertUser` fails the victim-name assertion at `:158`; dropping the
  check entirely fails all three. The positive test asserting a cookie *is*
  issued is what makes the two negative cookie assertions mean something.
- **Scope.** 234 added lines, zero removed, four files, no migration, no
  contract-adjacent code, nothing the issue did not ask for. `domain` was
  already imported in `handlers_sso.go:14`. No scope creep.
- **Already-issued JWTs are unaffected**, as the issue said. There is no
  server-side session store; `issueJWT` (`handlers_auth.go:139-155`) signs a
  7-day cookie and that signed token *is* the session. If this bypass was
  exploited before the fix, deploying it does not evict the attacker — the
  deployment needs a `jwtSecret` rotation to do that. That belongs in the
  release note for this PR, and it is the operator's call, not a code finding.

---

## Residual risk after this fix

Post-fix an attacker needs all of: (a) an account, which only
`handlers_auth.go:41-79` creates, from a Google-asserted email — so their
domain `D` is one they genuinely control; (b) `D` not in `genericDomains`
(`domain.go:5-17`), or `AssociateUserWithOrg` leaves `OrgID` nil and
`handlers_sso.go:203` returns 403 — gmail/outlook attackers are excluded
outright; (c) a provider row for some domain `P` with
`DomainMatchesOrg(P, D)` (`handlers_sso.go:238`), i.e. **`P == D` or `P` ends in
`"." + D`**; (d) an asserted email whose domain is exactly `P`.

So the blast radius is precisely: **a 7-day session for any account whose email
domain is the attacker's own org domain, or any subdomain of it — and nothing
else.** The pre-fix "any account on the deployment, including consumer-domain
accounts" is gone. What survives:

1. **Intra-domain escalation.** Any member of `acme.com` can overwrite
   `acme.com`'s provider row and mint a session as `ceo@acme.com`. There is no
   admin role, so "any member" is literal. This is **PAC-22**'s admin gate, and
   it is now the largest surviving item.
2. **Sibling-subdomain org takeover** — `DomainMatchesOrg` is a suffix match
   while every lookup is exact. New issue filed (below).
3. **Login-flow hijack.** Writing a provider row flips `DetectAuthProvider` for
   every user of that domain to `type:"sso"` pointing at the attacker's IdP, so
   the org's normal login button sends colleagues to an IdP the attacker runs.
   Post-fix that no longer mints a Paceday session for them, but it still
   harvests whatever they type, and it is a login denial-of-service for the
   domain. Also **PAC-22**.

One caveat worth recording so nobody re-derives it: (c) would widen to almost
everything if an attacker held an account at a public suffix or a very short
domain (`@com`, `@co.uk`), since `HasSuffix(victim, ".com")` is true for most of
the internet. Accounts come only from Google OAuth, so that requires actually
owning and verifying such an address with Google. Not practical, but it is the
reason the suffix match in (c) is worth removing rather than reasoning about.

Filed as a follow-up: **PAC-46** — `createSSOProvider` / `deleteSSOProvider`
authorize with a suffix match while every lookup is exact, letting an org member
write and silently delete a sibling subdomain org's SSO provider row.
