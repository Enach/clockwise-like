# Spec — PAC-89: SSO admin step-up verifies Microsoft ID tokens with the tenant-pinned verifier

- **Linear**: https://linear.app/paceday/issue/PAC-89/sso-admin-step-up-move-microsoft-id-token-verification-onto-pac-71s
- **Status**: challenged 2026-11-13 (accept-with-changes, applied in place; revision 2). Ready for contract.
- **Author**: spec-author; revision 2 by spec-challenger (see the Challenge log at the end)
- **Inherited scope**: none blocking. `contracts/openapi` carries no `x-uncertain` marker on the step-up operations. Audit rows touching the endpoints: API-001 (`docs/factory/api-audit.md:259`, write half closed by PAC-50) and API-092 (`api-audit.md:350`, step-up statuses asserted by PAC-50). This change adds no new status and no new documented outcome, so neither row moves.
- **Origin**: PAC-71 contract revision 3.1, item 3.1-10 (`contracts/features/PAC-71.md:542`) and candidate follow-up `stepupVerifierTenantPinned` (`contracts/features/PAC-71.yaml:303`), filed as PAC-89.
- **Verified against**: `clockwise-like` `origin/main` at `68540f6` (PAC-71 merged, #259 is its latest fix). No code was run and no provider endpoint was called. Library behaviour was read from `github.com/coreos/go-oidc/v3@v3.20.0` in the module cache.
- **Owner decisions applied (`.claude/factory/owner-decisions.md`, binding)**: PAC-50 admin proof by a Google Workspace / Microsoft 365 admin step-up (`:93-94`); PAC-71 personal Microsoft accounts refused until PAC-77 (`:109-110`); PAC-71 OQ-8 / OQ-9 Microsoft identity taken from the signed ID token, `openid profile`, no other new scope (`:113-123`).

## 1. Problem

Paceday checks Microsoft ID tokens in two places. Since PAC-71, sign-in uses a strict verifier pinned to the tenant. The SSO admin step-up (PAC-50), which unlocks writes to an organisation's SSO configuration and its AI provider key, still uses an older, looser check:

- it reads signing keys from Microsoft's shared multi-tenant key set rather than the token's own tenant's;
- it accepts any non-empty tenant id that has no `/`, `?` or `#`, rather than only a GUID;
- it does not require the v2.0 token format;
- it accepts an audience list that merely contains the client id;
- its expiry and not-before tolerances differ from sign-in's;
- it does not refuse the personal-account tenant at the token.

PAC-71's contract judged this **not exploitable today** (`contracts/features/PAC-71.yaml:303`). Microsoft serves the same keys at both addresses, and the token arrives directly from Microsoft's token endpoint over TLS rather than through the browser. The cost is different. The higher-privilege flow validates the same kind of token less strictly than the ordinary one. Two implementations of one security check can drift, and a reviewer has to reason about both. Each looser rule is also one less layer between a future Microsoft-side change (for example, tenant-specific signing keys for an app) and an admin grant. This ticket makes the step-up hold a Microsoft ID token to exactly the rules sign-in holds it to. Who counts as an administrator does not change.

**Assumed problem (stated plainly):** the issue asks for a solution ("use that verifier"). The problem recovered here is "one way to validate a Microsoft ID token, and the stricter one on the admin path". It is hardening, not a fix for a known exploit.

## 2. Current behaviour

### 2.1 Step-up (PAC-50) ID-token verification

- **Construction.** `newStepup` builds one `gooidc.NewRemoteKeySet` on `ep.JWKSURL` and a `gooidc.NewVerifier` with `ClientID` set and `SkipIssuerCheck: true` (`backend/auth/stepup.go:101-116`). The issuer is checked by hand afterwards (`:112-113`).
- **Key source.** For Microsoft, `ep.JWKSURL` is `https://login.microsoftonline.com/common/discovery/v2.0/keys` in production (`backend/config/idp.go:62`), or `<stand-in>/common/discovery/v2.0/keys` when `PACEDAY_MICROSOFT_IDP_BASE_URL` is set under declared test mode (`idp.go:87`, `:103-139`). It is one key set for every tenant, so there is no per-tenant cache. go-oidc's `RemoteKeySet` single-flights fetches (so 20 concurrent cold verifications already make one fetch), keeps keys with no TTL, and refetches on any unknown `kid` with no rate limit (`go-oidc/v3@v3.20.0/oidc/jwks.go:155-188`, `:199`). The key fetch uses the context the key set was built with (`stepup.go:103`), not the request's, so it is bounded only by the client's 15 s.
- **HTTP client and timeouts.** The client is `newIdPHTTPClient(ep.Guard)` (`stepup.go:102`, `:125-146`). It has a 10 s dial timeout, a 15 s overall `Client.Timeout` (`:141`), no proxy, no redirects, and the connection guard (`config.PermittedPeer`) only for a stand-in. This is **not** PAC-81's `outbound.IdentityClient()` (10 s, `backend/internal/outbound/outbound.go:28,39,88`), which sign-in uses. That client is a plain `http.Client` on a clone of `http.DefaultTransport` (`outbound.go:73-82`): it **honours the ambient proxy** and **follows redirects**, both of which the step-up client refuses.
- **Algorithm.** `SupportedSigningAlgs` is empty, so go-oidc accepts RS256 only (`go-oidc verify.go:296-303`). This matches PAC-71.
- **Audience.** go-oidc accepts `aud` when the client id is one of its entries, `slices.Contains` (`verify.go:251-258`), so a multi-element array passes.
- **Expiry / not-before.** `exp` has no leeway (`verify.go:269`). `nbf` has 5 minutes of leeway (`verify.go:274-281`).
- **Issuer and tenant.** `issuerOK` for Microsoft requires `tid != ""`, no `/?#` in `tid`, and `iss` **exactly** equal to the issuer template with `{tenantid}` replaced by `tid` (`stepup.go:243-245`). An `iss` naming another tenant than `tid` is therefore **already rejected** today. `tid` is not required to be a GUID, and its case is compared exactly.
- **`ver`, `oid`.** Not checked. `oid` is read into `StepupIdentity.ObjectID` (`stepup.go:191`, `:235`) and used only by the PAC-71 identity comparison in the handler (`backend/api/handlers_sso_stepup.go:636`).
- **Nonce.** It must equal the state's nonce and must not be empty (`stepup.go:228-230`). The comparison is not constant-time.
- **Ordering.** Every key fetch happens inside `Verify`, before any claim is examined (`stepup.go:217`). So a non-GUID `tid` still costs a key fetch. It cannot reach a URL, though, because the key URL does not depend on it.
- **Consumer tenant.** Not refused at the token. The step-up authorizes at the `organizations` authority (`idp.go:60-61`), and the handler then requires Graph `/organization` to return exactly one organisation whose id equals `tid` and whose verified domains contain the org domain (`handlers_sso_stepup.go:611-615`). A personal account would fail there, after a Graph call, with reason `tid_mismatch`.
- **Failure mapping.** Any exchange or verification error becomes the denial reason `token_invalid` (`handlers_sso_stepup.go:580-584`).

### 2.2 What the handler checks after verification (PAC-50 admin-role checks, to be preserved)

`checkStepup` (`handlers_sso_stepup.go:571-639`), Microsoft branch, in this order:
1. Graph `/organization`: the id equals `tid` (**exact, case-sensitive** `tid != id.TenantID`, `:613`), the org domain is verified, otherwise `tid_mismatch` (`:611-615`).
2. The org's pinned tenant: a different pin gives `tid_changed` (`:616-622`; **exact** comparison `:620`). The pin is then written by a conditional update that is **also exact**, `WHERE id = $1 AND (ms_tenant_id IS NULL OR ms_tenant_id = $2)` (`backend/storage/sso_stepup.go:215-226`); zero rows gives `ErrTenantPinned`, which the handler turns into `tid_changed` (`handlers_sso_stepup.go:687-690`). The only writer of `organizations.ms_tenant_id` (migration `030_sso_admin_stepup.up.sql:61`, `TEXT NULL`) is this path, storing Graph's id as returned.
3. `preferred_username` or `email` equals the session email, otherwise `email_mismatch` (`:623-625`).
4. `wids`, or Graph directory roles when `wids` is absent, must include a qualifying Entra role template, otherwise `not_admin` (`:626-634`; roles list `backend/auth/stepup.go:39-44`).
5. The PAC-71 identity binding (tenant lower-cased, `oid` lower-cased), otherwise `identity_mismatch` (`:636-638`).
6. A grant pins `tid` as Graph returned it (`:639`).

### 2.3 Sign-in (PAC-71) verifier

`MicrosoftIDTokenVerifier` (`backend/auth/ms_idtoken.go:69-111`, `:140-207`):
- compact JWS; `alg` must be `RS256`; a `kid` is required (`:141-154`);
- `tid` must be a canonical GUID **before any fetch** (`:159-163`) and is lower-cased (`:164`);
- keys come from `https://login.microsoftonline.com/{tid}/discovery/v2.0/keys` through the package-level seam `MicrosoftKeysURL` (`:47-49`), which tests repoint (`backend/api/pac71_msid_helpers_test.go:169-172`, `backend/auth/ms_idtoken_test.go:67-70`). The host is fixed: it does **not** follow `PACEDAY_MICROSOFT_IDP_BASE_URL` and has no connection guard;
- the RSA PKCS#1 v1.5 SHA-256 signature is checked by hand (`:169-176`);
- `iss` must be `https://login.microsoftonline.com/<tid>/v2.0`, with the tenant part compared case-insensitively and the host also fixed (`:177-179`, `:213-219`);
- `ver` must be `2.0` (`:180-182`); `aud` must be the client id as a string or a one-element array (`:183-185`, `:231-244`); `oid` must be a GUID (`:186-188`);
- `exp` is required and `exp` and `nbf` each get 60 s of skew (`:189-202`, `msClockSkew` `:42`);
- nonce is compared in constant time and must not be empty (`:203-205`);
- **cache**: per tenant, 24 h TTL, unknown-`kid` refetch at most once a minute, at most 1000 tenants with LRU eviction, single-flight per tenant whose fetch is detached from the requesting context (`:36-43`, `:250-338`);
- **fetch**: timeout `outbound.Current().Identity` (10 s), client `outbound.IdentityClient()` unless one is injected, body capped at 1 MiB (`:357-401`);
- returns only `TenantID` and `ObjectID` (`:63-67`). The step-up also needs `email`, `preferred_username` and `wids` (`stepup.go:172-186`), which this result does not carry;
- the consumer tenant is refused by the **sign-in handler** after verification, not by the verifier (`backend/api/handlers_pac71_auth.go:252-257`; constant `ms_idtoken.go:34`);
- one process-wide instance, `microsoftIDTokens` (`handlers_pac71_auth.go:602`).

### 2.4 Tests that exist today

- Microsoft step-up handler tests drive a stand-in IdP (`backend/api/pac50_sso_helpers_test.go:162-200`). Its tokens carry **non-GUID tenants** (`"t1"`, `"t2"`, `"tenant-abc"`, `backend/api/pac50_sso_stepup_test.go:203-207`, `:260`), **no `ver`**, and `iss` derived from `tid` (`helpers:189-192`). Its keys are served at the stand-in's `/common` path.
- Denials covered: `tid_mismatch` ×3, `tid_changed`, `not_admin` ×2, `email_mismatch`, `token_invalid` for a provider error only (`pac50_sso_stepup_test.go:195-255`). Grant and tenant pin: `:256`. Step-up reservation tests: `backend/api/pac90_test.go:245-246`.
- Other tests drive the same Microsoft stand-in with non-GUID values: the PAC-71 check-4b test `backend/api/pac71_address_test.go:297-318` (tenant `"tenant-abc"`/`"tenant-other"`, `oid` `"O2"` and `"O1-<uuid>"`, none a canonical GUID), and `pac90_test.go:246` and `backend/auth/pac50_signin_scopes_test.go:41-42` construct `NewMicrosoftStepup` directly. Under the new rules the 4b `identity_mismatch` case (`oid` `"O2"`) would become `token_invalid`, so its fixtures must change for its assertion to keep meaning anything.
- **No test** sends the step-up a token whose `iss` disagrees with `tid`, a wrong signature, a wrong `aud`, a wrong nonce, an expired token, or a wrong `alg`. Verification-level denials are untested at handler level. That is itself a finding.
- No e2e covers the Microsoft step-up. The e2e stack configures Google only (`e2e/tests/sso-stepup.spec.ts:66`, `e2e/docker-compose.e2e.yml:131`).
- The sign-in verifier has unit tests (`backend/auth/ms_idtoken_test.go`) and handler tests (`backend/api/pac71_msid_test.go`).

## 3. Desired behaviour

A Microsoft step-up's ID token is accepted only if it would pass every check that a Microsoft sign-in's ID token must pass:
- RS256 only, with a key id;
- a GUID tenant, checked before anything is fetched;
- the signature by a key published for that same tenant;
- an issuer naming that tenant, in the v2.0 format;
- the audience exactly the application;
- a GUID object id;
- expiry and not-before within the same 60 s tolerance;
- the nonce of this step-up.

The checks come from one shared implementation, not a copy. Signing keys are cached per tenant with the same lifetime, refetch limit, tenant cap and single-flight behaviour. Each fetch is bounded by the same identity-provider time limit.

A token from Microsoft's personal-account tenant is refused for the step-up, before Paceday asks Microsoft anything else.

Every token-level refusal is the step-up's existing "token invalid" denial: no grant, one denied event, and the user lands back on settings with that reason. A refusal is never a server error.

After the token is accepted, nothing about who counts as an administrator changes:
- the organisation and verified-domain check;
- the tenant pin;
- the email match;
- the role list and its Graph fallback;
- the identity binding.

The scopes requested do not change. Neither does the Google step-up.

A test-mode deployment that redirects Microsoft to a stand-in keeps working. The step-up's per-tenant keys and issuer follow that redirection, under the same connection guard, with no proxy and no redirect followed (today's step-up client properties). A production deployment fetches only from Microsoft's per-tenant key address.

Keys fetched from one key address never satisfy a verification that expects another: a cached entry is identified by the key address it came from, not by the tenant alone. In a test-mode deployment, keys the step-up fetched from the stand-in are never used by sign-in, which still expects Microsoft's address (§5), and the reverse.

Tenant ids are equal when they are equal ignoring case, at every place the step-up compares them: the token's tenant against Graph's organisation id, against the org's existing pin, and in the conditional write of the pin. A pin written by a grant is the lower-case GUID. Existing pins are not rewritten by a migration (§9 D-6).

## 4. Acceptance criteria

Test levels: `[unit]` = a Go test of the verification logic alone, with a stub key server. `[contract]` = a Go test through the real router and the step-up callback, with the stand-in IdP, asserting the denial or grant, the grant count, the event row and the outbound requests. No `[e2e]`: the e2e stack has no Microsoft identity provider (§2.4), and adding one is out of scope (§5).

**Fixtures.** Every Microsoft tenant and object id in a fixture that is meant to pass is a canonical 8-4-4-4-12 hex GUID generated per test (`uuid.NewString()`) or a fixed literal GUID, never a label such as `t1` or `tenant-abc`, and never the consumer tenant `9188040d-6c67-4c5b-b112-36a304b66dad` except in AC-9. At least one passing fixture spells its tenant GUID in upper-case hex in the token and/or the Graph answer (AC-17). A test that counts key fetches builds a fresh verifier or provider, because a key set warmed by an earlier step-up hides fetches.

"Denied as token-invalid" below means: a redirect to the settings landing with the existing token-invalid reason, zero live grants, exactly one denied step-up event with that reason, the tenant pin unchanged, and **zero** Graph requests.

AC-1. Given a Microsoft step-up whose ID token has a GUID `tid` A, a valid signature by A's key, and an `iss` naming tenant B ≠ A, when the callback runs, then it is denied as token-invalid. `[contract]` *(This passes today because of the exact issuer comparison (§2.1). It is required by the issue as a regression guard and is not falsifiable today. AC-2 to AC-12 and AC-17 are, except the halves each marks as a guard; AC-13 to AC-16 are guards; AC-18 is falsifiable against a naive shared cache.)*

AC-2. Given a token for tenant A signed by a key that the stand-in publishes only at the shared multi-tenant key address and not at tenant A's address, when the callback runs, then it is denied as token-invalid. A token signed by a key published at tenant A's address is accepted, other checks permitting. `[contract]`

AC-3. Given a token whose `tid` is not a canonical GUID (`t1`, `common`, `organizations`, a value containing `/` or `..`, empty, absent), when the callback runs, then it is denied as token-invalid **and** the key server records zero requests for that callback. `[contract]` + `[unit]`

AC-4. Given a token with `ver` absent or `1.0`, otherwise valid, then denied as token-invalid. `[contract]`

AC-5. Given a token whose `aud` is a two-element array containing the client id, otherwise valid, then denied as token-invalid. Given `aud` as the client id string, or as a one-element array of it, it is accepted. `[contract]`

AC-6. Given a token whose `oid` is absent or not a GUID, otherwise valid, then denied as **token-invalid**. Today it reaches the identity comparison instead. `[contract]`

AC-7. Given a token with `exp` 30 s in the past, it is accepted. With `exp` 90 s in the past, it is denied as token-invalid. Given `nbf` 2 minutes in the future, it is denied as token-invalid. The clock is injected. `[contract]`

AC-8. Given a token signed with RS256 but whose header names another algorithm, or that has no `kid`, then denied as token-invalid. Given a nonce different from the step-up's, or no nonce, then denied as token-invalid. `[contract]` *(The `alg` and nonce halves are regression guards. The missing-`kid` half is falsifiable today: go-oidc tries every cached key when the header has no `kid`, so a `kid`-less token signed by a published key is accepted. (`go-oidc/v3@v3.20.0/oidc/jwks.go:157-165`).)*

AC-9. Given a validly signed v2.0 token whose `tid` is Microsoft's personal-account tenant (`9188040d-6c67-4c5b-b112-36a304b66dad`), and a stand-in whose Graph `/organization` would answer that id with the org domain verified, when the callback runs, then it is denied with reason **`tid_mismatch`** (§9 D-4), zero live grants, one denied event, pin unchanged and **zero** Graph requests. `[contract]` *(Falsifiable today: today this fixture is granted.)*

AC-10. Given two Microsoft step-ups for the same tenant within 24 hours, then the tenant's keys are fetched once. Given 20 concurrent step-up verifications for a tenant that is not cached, then exactly one key fetch is made, and all 20 complete. Given a `kid` unknown to a cached tenant, then at most one refetch is made per minute. Given tokens for tenants A and B, then one fetch is made to A's key address and one to B's. `[unit]` *(The first two clauses are regression guards: go-oidc already caches without TTL and single-flights (§2.1). The rate-limit clause and the per-tenant clause are falsifiable today: the shared key set refetches on every unknown `kid` and fetches only the multi-tenant address.)*

AC-11. Given the identity-provider limit set to 300 ms for the test (`outbound.SetForTest`) and a key server that accepts the connection but never answers, when a step-up callback verifies, then it answers the token-invalid redirect within 2 s, never a 5xx. *(Falsifiable today: the step-up's key fetch ignores the identity limit and waits for its own 15 s client timeout or the request budget.)* Given a key server that answers 500 or a body over 1 MiB, then denied as token-invalid. `[contract]`

AC-12. Given a deployment with the Microsoft identity provider redirected to a stand-in under declared test mode, then every step-up key fetch goes to the stand-in, under the tenant's own path, through the connection guard, and none goes to Microsoft's host. The expected issuer is the stand-in's per-tenant issuer. Given a production deployment, then the key fetch address is exactly Microsoft's per-tenant key address for the token's tenant. `[unit]`

AC-13. Given a Microsoft administrator whose token passes every check (GUID tenant, `ver` 2.0, GUID `oid`), when the step-up completes, then a grant is issued and the tenant is pinned, with or without `wids` (Graph fallback). This is PAC-50 AC-9b/AC-11a/AC-11b, re-run with fixtures that now carry GUID tenants, `ver` and a GUID `oid`. The pinned tenant is lower-case. `[contract]`

AC-14. Given each existing Microsoft denial case (`tid_mismatch` ×3, `tid_changed`, `not_admin` ×2, `email_mismatch` in `pac50_sso_stepup_test.go:195-255`; and the three Microsoft check-4b cases of `pac71_address_test.go:297-318`: bound `oid` granted, other GUID `oid` `identity_mismatch`, bound in another GUID tenant granted), when re-run with conforming GUID fixtures, then each yields the same outcome and reason as today. `[contract]`

AC-15. The Google step-up's tests and the PAC-71 sign-in tests (`backend/auth/ms_idtoken_test.go`, `backend/api/pac71_msid_test.go`, and every non-Microsoft-step-up case of `pac71_address_test.go`) pass with **no change to their assertions or fixtures**. `[contract]`

AC-16. The scopes that each step-up's authorization request carries, and its listed extra scopes, are byte-identical to today's (PAC-50 AC-24). `[unit]` *(Guard. `pac50_signin_scopes_test.go` assertions unchanged; its provider construction lines may change only if the constructor's signature does.)*

AC-17. Tenant case. (a) Given an org pinned to tenant GUID G spelled in upper-case hex (seeded directly), and an otherwise conforming step-up whose token `tid` and Graph id are G in lower case, then it is **granted**, and the stored pin afterwards equals lower(G). (b) Given no pin and a Graph `/organization` id equal to the token's `tid` but in the other case, then granted, and the pin is lower(G). (c) Given a pin of another GUID, then `tid_changed` as today, pin unchanged. `[contract]` *(a and b are falsifiable today: both comparisons are exact.)*

AC-18. Given a test-mode deployment whose Microsoft provider is redirected to a stand-in, after a step-up for tenant A has cached the stand-in's keys for A, when a sign-in ID token for A signed by that stand-in key is verified by sign-in's verifier, then it is refused, and sign-in's fetch goes to its own key address (`MicrosoftKeysURL(A)`), not served from the step-up's entry. `[unit]`

## 5. Explicitly out of scope

- **The Google step-up.** Its verification is unchanged. Google has one issuer and one key set, so tenant pinning does not apply.
- **Who counts as an administrator.** The qualifying roles, the Graph fallback, the organisation and verified-domain rule, the tenant pin and the email match are PAC-50's and stay as they are (§2.2).
- **Making sign-in follow the stand-in redirection.** Sign-in's verifier hard-codes Microsoft's host, and sign-in's own authorization addresses are not redirected either. Changing that is a separate e2e-enablement question (§8 OQ-F2).
- **A Microsoft stand-in in the e2e stack.** That would let a browser test cover the Microsoft step-up. It is worth having, but it is its own ticket.
- **Personal Microsoft accounts (PAC-77).** This ticket only keeps refusing them on the step-up path.
- **New scopes or consent.** None. The step-up already requests `openid` and `profile`.
- **Removing the Graph `/organization` cross-check.** The token now proves the tenant, but the check also proves the org domain is verified in that tenant, which the token does not.

## 6. What must not change

- PAC-50's Microsoft step-up outcomes for conforming tokens: `pac50_sso_stepup_test.go:195-255` (denials) and `:256` (grant and pin), and `pac90_test.go` (reservation). Their **stand-in fixtures must change**: GUID tenants, a `ver` claim, a GUID `oid`, keys served under the tenant's path. Their **assertions must not**. A reviewer should diff the assertions separately from the fixtures.
- The Google step-up: every Google case in `pac50_sso_stepup_test.go` unchanged, fixtures included.
- PAC-71 sign-in: `backend/auth/ms_idtoken_test.go`, `backend/api/pac71_msid_test.go` unchanged. The Microsoft check-4b subtest of `backend/api/pac71_address_test.go:297-318` is a step-up test: its **fixtures** change to GUIDs (tenant, bound `oid`, other `oid`, other tenant), its three expected outcomes do not. Its Google subtest is unchanged. Sign-in's consumer-tenant refusal stays in sign-in (`handlers_pac71_auth.go:252-257`).
- Step-up scopes: `backend/auth/pac50_signin_scopes_test.go:41-88`.
- Step-up stand-in configuration rules (test mode required, guard on): `backend/config/idp_test.go`.
- Not protected by any test today: a token-level denial of the Microsoft step-up (§2.4). AC-1 to AC-9 add that protection.

## 7. Rollback

Revert the commit. There is no migration. The only data change is that a grant rewrites an existing pin that differed only in case to its lower-case form (AC-17a). The reverted exact comparisons (`handlers_sso_stepup.go:613`, `:620`, `storage/sso_stepup.go:218`) still match it as long as Graph answers lower-case ids, as it does in practice; were Graph to answer upper-case, a reverted build would deny that org `tid_changed` until the pin is corrected by the operator. Grants issued under either version remain valid until they expire.

## 8. Open questions

No owner (product-level) question. Every product rule involved has an owner decision (header). The factory-level questions below are for the contract author.

- **OQ-F1: resolved (challenge 2026-11-13).** Case-insensitive comparison at all three sites (§2.2: `:613`, `:620`, `storage/sso_stepup.go:218`), new pins lower-case, no migration (D-6, AC-17). Existing rows: the only writer stores Graph's `/organization` id, which Microsoft returns as a lower-case GUID in practice; the only non-GUID or mixed-case values the code has ever written come from test fixtures. No production database is reachable from the factory to prove it, so the comparison is made case-insensitive rather than relying on it, which makes a migration unnecessary.
- **OQ-F2 (orchestrator, non-blocking): sign-in under a stand-in.** AC-12 makes the step-up's verifier follow the Microsoft redirection, but sign-in's does not (§2.3). Whether sign-in should follow it too is an e2e-enablement question; file as a follow-up if wanted. Not needed here, and AC-18 keeps the two from sharing keys meanwhile.
- **OQ-F3: resolved (challenge 2026-11-13).** Sharing is allowed only between verifications that expect the same key address (in production, both flows); entries are identified by key address (D-2, AC-18). Whether that is one instance or two is a plan choice, provided AC-10 and AC-18 hold and PAC-71's test seams (`auth.MicrosoftKeysURL`, `api.microsoftIDTokens`, `pac71_msid_helpers_test.go:169-172`) keep working unchanged.

## 9. Decisions

| # | Decision | Source |
|---|---|---|
| D-1 | The step-up uses the **same implementation** of the Microsoft ID-token rules as sign-in (§2.3), extended to hand back the claims the admin checks need (email, preferred username, roles and whether roles were present). A second copy of the rules is not acceptable. | factory-assumed (issue scope; PAC-71 3.1-10 "one way") |
| D-2 | The key cache may be shared with sign-in only where both expect the same key address; an entry is identified by its key address, so a stand-in deployment never mixes with production entries (AC-18). Sign-in keys its cache by tenant alone today (`ms_idtoken.go:83`, `:253`), so sharing as-is would let a stand-in step-up's keys serve a sign-in for the same tenant. | factory-assumed; challenge 2026-11-13 |
| D-3 | The step-up's per-tenant key address and expected issuer follow the existing Microsoft identity-provider decision (production → Microsoft; stand-in only under declared test mode, with the connection guard). Sign-in's behaviour is not changed by this ticket. | factory-assumed |
| D-4 | The consumer tenant is refused on the step-up after token verification and before any Graph request. The denial reason is the existing `tid_mismatch` (the tenant cannot be an organisation that administers the org domain), so no new reason value reaches the web client. | consumer refusal: **owner** (PAC-71, personal Microsoft accounts refused until PAC-77, 2026-11-01), applied to the step-up by the factory; reason value factory-assumed |
| D-5 | Every token-level refusal maps to the existing `token_invalid` denial, as today's verification errors do (`handlers_sso_stepup.go:580-584`). No new status, reason or contract change. | factory-assumed |
| D-6 | Tenant ids are compared case-insensitively at the Graph comparison, the pin check and the conditional pin write; a grant stores the lower-case GUID; no migration (OQ-F1, AC-17). | factory-assumed; challenge 2026-11-13 |
| D-7 | Behaviour tightened deliberately, accepted as consequences of D-1: a multi-element `aud` is refused, `ver` 2.0 is required, a GUID `oid` is required, the `nbf` tolerance drops from 5 minutes to 60 s, and `exp` gains 60 s. | factory-assumed (PAC-71 contract rules) |
| D-8 | Scopes unchanged. The step-up already requests `openid profile`. The owner's `openid`/`profile` decision concerns sign-in and needs nothing new here. | **owner** (PAC-71 OQ-8/OQ-9, 2026-11-01) |
| D-9 | Admin-role checks (§2.2) are unchanged. | **owner** (PAC-50) / issue scope |
| D-10 | The Graph `/organization` cross-check stays (§5). | factory-assumed |
| D-12 | HTTP client of the step-up key fetch. Production: the identity class of PAC-81 (10 s); following a redirect or the ambient proxy is accepted there only if the plan shares sign-in's client, since the host is fixed to Microsoft over TLS, and the plan must say which it does. Stand-in: today's step-up client properties are kept (guard, no proxy, no redirect) and the fetch is also bounded by the identity class (AC-11, AC-12). | factory-assumed; challenge 2026-11-13 |
| D-13 | The step-up keeps requiring exactly what sign-in requires of a v2.0 ID token, and this is judged safe for real administrators: the step-up authorizes at Microsoft's v2.0 `organizations` endpoint, whose ID tokens carry `ver` `"2.0"`, a single-string `aud` equal to the client id, a GUID `tid`, and a GUID `oid` because `profile` is requested (`stepup.go:79`). The Graph scopes affect only the access token. The narrower `nbf` tolerance needs the server clock within 60 s of Microsoft's, as sign-in already does. Not covered and not changed: an app configured with app-specific signing keys (published only with `?appid=`), which sign-in also does not support. | factory-assumed; challenge 2026-11-13 |
| D-11 | The issue's requested test (an `iss` naming another tenant than `tid`) is kept as AC-1, explicitly marked as a regression guard because it already passes today. | factory-assumed |

## Challenge — 2026-11-13

**Verdict**: accept-with-changes (changes applied in place above as revision 2; no owner question raised).

Checked against `clockwise-like` `origin/main` `68540f6`, `smart-calendar-flow` `origin/main`, and `go-oidc/v3@v3.20.0` in the module cache. Every §2 citation was opened.

### Blocking (all resolved in revision 2)
1. **Tenant case was fixed at one site of three.** D-6 named only `handlers_sso_stepup.go:620`. The Graph comparison `:613` is also exact, and the pin is written by a conditional SQL update that compares exactly (`storage/sso_stepup.go:215-226`), whose miss becomes `tid_changed` (`handlers_sso_stepup.go:687-690`). Once the verifier lower-cases `tid`, a Graph answer or legacy pin in another case would deny real administrators. Resolved: §2.2, §3, D-6 cover all three; AC-17 tests them; OQ-F1 closed (no migration; reason recorded).
2. **AC-14/AC-15/§6 contradicted the code.** `pac71_address_test.go:297-318` (a PAC-71 file) drives the Microsoft step-up with `tid` `tenant-abc` and `oid` `O2`/`O1-<uuid>`. Under the new rules those are `token_invalid`, so "PAC-71 tests unchanged" and "`identity_mismatch` keeps its reason" could not both hold. Resolved: §2.4, §6, AC-14, AC-15 move that subtest to "fixtures change, outcomes don't".
3. **D-2 as written was unsafe in test mode.** Sign-in's cache is keyed by tenant alone (`ms_idtoken.go:83`, `:253`). Sharing the instance would let keys a stand-in served to the step-up satisfy a sign-in for the same tenant. Resolved: D-2 requires entries identified by key address, AC-18 tests it, OQ-F3 closed.
4. **AC-11 was not falsifiable** ("10 s plus the existing request budget" names no budget). Resolved: concrete limit via `outbound.SetForTest`, bound 2 s; falsifiable because today's fetch ignores the identity limit.

### Non-blocking
1. **AC-10 mislabelled.** go-oidc already single-flights and caches without TTL (`jwks.go:155-188`, `:199`), so the concurrent clause is a guard, not falsifiable. Relabelled. A per-tenant clause was added, which is falsifiable.
2. **Client properties were unstated.** Sign-in's `outbound.IdentityClient()` honours the ambient proxy and follows redirects (`outbound.go:73-82`); the step-up client does neither. D-12 states what stays the same in test mode and what the plan must declare in production.
3. **AC-9's reason was indirect.** It is now `tid_mismatch` explicitly, and the fixture is pinned so the AC fails today.
4. **Fixtures.** Real canonical GUIDs are now required, with one upper-case spelling and fresh verifiers for fetch counts (§4 "Fixtures").
5. **Contract prose.** `contracts/openapi/paths/auth.yaml` (microsoftOAuthCallback step 2) says the step-up verifier "reads the `/common` keys". That becomes stale. The text is mirrored into the web's generated types (`src/api/generated/schemas.types.d.ts:4484-4485`). Left to the contract author: see `contracts/features/PAC-89.md`.

### D-1 / D-7: will real administrators be refused?
No more than at sign-in, which already enforces these rules on tokens from the same endpoint family. The step-up authorizes at `login.microsoftonline.com/organizations/oauth2/v2.0/*` (`config/idp.go:60-61`), which issues v2.0 ID tokens with:
- `ver` `"2.0"`;
- a single-string `aud` equal to the client id (the Graph scopes `User.Read`, `Organization.Read.All` and `RoleManagement.Read.Directory` shape the access token, not the ID token);
- a GUID `tid` and `iss` `.../{tid}/v2.0`;
- `oid`, because `profile` is requested (`stepup.go:79`; PAC-71 relies on the same fact, `contracts/features/PAC-71.md` revision 3.1).

Residual risks are recorded in D-13: server clock skew over 60 s, and app-specific signing keys, both shared with sign-in.

### D-3 / OQ-F2: stand-in safety
A stand-in is reachable only through `DecideMicrosoftIdP`. That refuses a base URL unless test mode is declared, and sets `Guard` (`config/idp.go:103-133`). The guard admits only loopback, RFC 1918 and ULA peers (`config/endpoints.go:53-79`). The tenant reaches the per-tenant path only after the GUID check (AC-3), so no path injection is possible. A production deployment has no stand-in and fetches only Microsoft's per-tenant address (AC-12). OQ-F2 stays an optional follow-up.

### D-4: consumer tenant reuses `tid_mismatch`
No new value reaches the web. `smart-calendar-flow` maps `tid_mismatch` to the copy "the Microsoft 365 organisation you used does not have your organisation's domain verified" (`SsoAdminStepupSection.tsx:33`, `OrganizationAISection.tsx:52`). That copy is acceptable for a personal account. Such a token cannot come from the real `organizations` authority, so this is defence in depth. The only observable change is that no Graph request is made.

### Criteria I could not falsify
- AC-1, the `alg`/nonce halves of AC-8, the first two clauses of AC-10, and AC-13 to AC-16 are guards. Each is labelled as one.

### Citations I checked and found wrong
- `go-oidc jwks.go:89-118` for refetch behaviour: those lines are the `inflight` type. The refetch is at `:155-188` and `:199`. Corrected.
- §6 "PAC-71 sign-in tests unchanged" overlooked `pac71_address_test.go:297-318`, which exercises the step-up. Corrected.
- Everything else in §2 matched the code. That includes `stepup.go:101-116`, `:125-146`, `:228-245`, `idp.go:62`, `:87`, `ms_idtoken.go:34-49`, `:140-207`, `:250-401`, `handlers_sso_stepup.go:571-639`, `handlers_pac71_auth.go:252-257`, `:602`, and `pac50_sso_stepup_test.go:195-260`.

### Consumers this breaks
- `clockwise-like/backend/api/pac50_sso_stepup_test.go`, `pac50_sso_helpers_test.go`, `pac71_address_test.go:297-318`, `pac90_test.go:246`: Microsoft fixtures change (by design); assertions do not.
- `smart-calendar-flow`: none. No new reason, status or field.

