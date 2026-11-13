# Implementation plan: PAC-89 (Microsoft step-up on the tenant-pinned verifier)

- **Spec**: docs/specs/PAC-89.md (revision 2, challenged 2026-11-13)
- **Contract**: contracts/features/PAC-89.{md,yaml} at ae84e8b (interface revision 1; bundle sha256 `5367da00…3328`, unchanged)
- **Resolves**: PAC-71 follow-up `stepupVerifierTenantPinned` (3.1-10)

## 1. Approach

The step-up's Microsoft branch stops using go-oidc and calls PAC-71's
`MicrosoftIDTokenVerifier`, the one implementation of the rules. The verifier
gains three per-instance parameters: the per-tenant key address (`KeysURL`), the
issuer template (`Issuer`) and the HTTP client (`HTTPClient`, which it already
had). It also gains `VerifyClaims`, which runs exactly `Verify`'s rules and then
returns the claims the admin checks read (email, preferred_username, wids and
whether wids was present). The key cache is keyed by key address, not by
tenant. Sign-in's defaults are unchanged: `MicrosoftKeysURL`, Microsoft's
issuer, and `outbound.IdentityClient()`.

`config.IdPEndpoint` gains `TenantKeysURL`, Microsoft's per-tenant key template:
production `https://login.microsoftonline.com/{tenantid}/discovery/v2.0/keys`,
stand-in `<base>/{tenantid}/discovery/v2.0/keys`. No existing value changes.

The handler refuses the consumer tenant as `tid_mismatch` before Graph (step 1a).
It compares the token's tenant with Graph's id and with the pin using
`EqualFold`. The conditional pin write compares with `lower()` and stores
`lower($2)`.

## Plan questions

- **One shared cache instance or two (AC-18)?** Two. Each Microsoft step-up
  provider owns its own verifier instance, and sign-in keeps
  `api.microsoftIDTokens`. This keeps PAC-71's seams and `Fetches()` counts
  untouched (AC-15). It costs at most one extra key fetch per tenant per 24 h.
  Entries are still keyed by key address, so AC-18 holds even if the instances
  are later merged. The unit test proves this on one instance as well.
- **HTTP client of production key fetches (D-12)?** The step-up's own client
  (`newIdPHTTPClient`) in production and in stand-in mode: no ambient proxy, no
  redirects, and the connection guard when a stand-in is declared. Each fetch is
  bounded by `outbound.Current().Identity` (10 s) through the fetch context. It
  is not sign-in's `outbound.IdentityClient()`.

## 2. Tests

| Test | File | AC |
|---|---|---|
| TestPAC89_AC1/2/3/4/5/6/7/8/9/11/13/17_* | backend/api/pac89_stepup_test.go (new) | 1-9, 11, 13, 17 |
| TestPAC89_StepupReturnsTheAdminClaims, _AC3_, _AC10_, _AC12_, _AC18_ | backend/auth/pac89_stepup_idtoken_test.go (new) | 3, 10, 12, 18 |
| Existing PAC-50 / PAC-71 4b Microsoft step-up tests, GUID fixtures | pac50_sso_helpers_test.go, pac50_sso_stepup_test.go, pac71_address_test.go | 13, 14 |
| ms_idtoken_test.go, pac71_msid_test.go, Google step-up, pac50_signin_scopes_test.go untouched | — | 15, 16 |

No migration. No generated code. No web change.

## 3. Rollback

Revert the PR. A grant may have rewritten a pin to lower case. The reverted
exact comparisons still match that pin while Graph answers lower-case ids
(spec section 7).
