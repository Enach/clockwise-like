# Contract: PAC-89 (Microsoft step-up ID tokens on the tenant-pinned verifier)

- **Linear**: PAC-89, https://linear.app/paceday/issue/PAC-89/sso-admin-step-up-move-microsoft-id-token-verification-onto-pac-71s
- **Spec**: `docs/specs/PAC-89.md` revision 2, challenged 2026-11-13
  accept-with-changes (applied in place; OQ-F1 and OQ-F3 resolved, OQ-F2
  optional follow-up).
- **Stage**: contract-author, revision 1, 2026-11-13. Awaiting contract-challenger.
- **Manifest**: `contracts/features/PAC-89.yaml`.

Verification (worktree of `factory/pac-89-spec`, Windows, Python 3):
`scripts/openapi_assemble.py --check` rc 0 (`OK: bundle is in sync (108 paths)`),
`scripts/openapi_migration_report.py --check` rc 0 (`OK: MIGRATION.md covers all
130 operations (12 generated)`), `scripts/openapi_gen_go.sh --check` rc 0
(`OK: backend/api/gen matches the contract`). `git diff origin/main --
contracts/openapi` is empty. Bundle sha256 on origin/main 68540f6 (last bundle
change b0c0d7a, PAC-97): `git show origin/main:contracts/openapi/openapi.yaml |
sha256sum` =
`5367da004130aa9037752baa1e17be951f9d4130d46e0cf0ce6e467975603328`, unchanged.

## 1. What changed in the OpenAPI contract

Nothing. `ssoAdminStepupCallback` (`contracts/openapi/paths/auth.yaml:2161`)
already promises what PAC-89 delivers. Here is how each part of PAC-89 maps to it:

- **Step 1.** "The authorization code is exchanged and the ID token verified
  (signature, audience, issuer, expiry) ... any failure here gives
  `token_invalid`". PAC-89 makes the checks stricter. It adds no outcome.
- **Step 2.** "the organisation answer's id equals the token's `tid` ...
  (`tid_mismatch`)". The personal-account tenant cannot equal an organisation
  that administers the org domain, so refusing it before the Graph call
  (spec D-4) is the same documented outcome, `tid_mismatch`. The difference
  is that one Graph request is not made.
- **Pinning.** "On success the org's tenant is pinned (idempotent)" still
  holds. The pin is now stored as the lower-case GUID and compared ignoring case
  (spec D-6). Neither value is visible at the HTTP boundary.

No status, reason value, query parameter, header or schema changes. The web
needs no regeneration.

## 2. Non-HTTP interface (binding for the PR)

### 2.1 What a Microsoft step-up ID token must satisfy

The rules are exactly those of `microsoftOAuthCallback` step 2 (`auth.yaml:457-510`,
PAC-71 3.1-7 to 3.1-9), applied by **one** implementation (spec D-1).

The nonce is the one exception. The expected value is the caller's: sign-in
passes the SHA-256 of its state, and the step-up passes its state cookie's
`Nonce`, as today. The comparison is constant-time, and an empty value is
refused.

Three things are parameterised:

| Parameter | Production | Stand-in (declared test mode only) |
|---|---|---|
| Key address | `https://login.microsoftonline.com/{tid}/discovery/v2.0/keys` | `<PACEDAY_MICROSOFT_IDP_BASE_URL>/{tid}/discovery/v2.0/keys` |
| Expected issuer | `https://login.microsoftonline.com/{tid}/v2.0` (tenant part case-insensitive) | `<base>/{tid}/v2.0`, the same comparison |
| HTTP client | identity class, 10 s (`outbound.Current().Identity`) | guarded (`config.PermittedPeer` per connection), no proxy, no redirect, also bounded by the identity class |

`{tid}` is substituted only after the canonical-GUID check. Sign-in keeps its
production values and its `auth.MicrosoftKeysURL` seam unchanged. The
production key address of both flows is the same string.

The verified result hands the step-up what it reads today:

- `tid`, lower-case;
- `oid`, lower-case;
- `email`;
- `preferred_username`;
- `wids`, and whether `wids` was present.

`StepupIdentity` keeps its fields. `TenantID` is lower-case from now on.

### 2.2 Key cache

- An entry is identified by **(key address)**. The tenant is part of the
  address, so this is per tenant per origin.
- The limits are PAC-71 3.1-8: 24 h TTL, one unknown-`kid` refetch per minute,
  1000 entries with LRU eviction, and single-flight per entry.
- One instance or two is the plan's choice. If the instance is shared with
  sign-in, AC-18 must hold: a stand-in entry never serves a production lookup.
- PAC-71's test seams keep working unchanged:
  - `auth.MicrosoftKeysURL`;
  - `api.microsoftIDTokens`, which `pac71_msid_helpers_test.go:169-172`
    replaces;
  - `Fetches()` and `Cached(tid)`, which `ms_idtoken_test.go` uses.
- If sharing makes `Fetches()` count step-up fetches in a test that also signs
  in, that test's expectation is a fixture question for the plan. It is not a
  reason to change PAC-71's assertions (AC-15).

### 2.3 Callback order (Microsoft branch), after state check 0

| # | Check | Failure reason | Graph requests on failure |
|---|---|---|---|
| 1 | Code exchange, then the ID-token rules of 2.1 | `token_invalid` | 0 |
| 1a | `tid` ≠ `9188040d-6c67-4c5b-b112-36a304b66dad` (consumer tenant) | `tid_mismatch` | 0 |
| 2 | Graph `/organization` id equal to `tid` **ignoring case**, and org domain verified | `tid_mismatch` | 1 |
| 2b | Existing pin equal to `tid` **ignoring case** | `tid_changed` | 1 |
| 3 | `preferred_username` / `email` equal to session email | `email_mismatch` | unchanged |
| 4 | `wids`, or the Graph role lookup, qualifies | `not_admin` | unchanged |
| 4b | PAC-71 identity binding (lower-case tenant and `oid`) | `identity_mismatch` | unchanged |
| 5 | Grant. The pin is written as the lower-case GUID by a conditional write whose guard also compares ignoring case. A lost race gives `tid_changed` as today. | — | — |

Today's PAC-81 budget registrations stay as they are:

- `running(token_invalid)` before step 1;
- `running(tid_mismatch)` before step 2;
- `running(not_admin)` before step 4.

Step 1a runs after step 1 and needs no registration of its own, because it
makes no outbound call.

### 2.4 Data

There is no migration. `organizations.ms_tenant_id` stays `TEXT NULL`.

- A pin that differs from the token's tenant only in case is honoured.
- A granted step-up rewrites that pin to lower case (spec AC-17a).
- Existing non-GUID pins are left in place. Only test fixtures have ever
  written one. Such a pin can never equal a GUID `tid`, so it gives
  `tid_changed`, which is today's behaviour for any mismatched pin.

## 3. Test map

| AC | Level | Where |
|---|---|---|
| 1-9, 11, 13, 14, 17 | contract (router + stand-in, Docker Postgres) | `backend/api/pac89_stepup_test.go` (new) plus the fixture changes in `pac50_sso_helpers_test.go`, `pac50_sso_stepup_test.go`, `pac71_address_test.go:297-318`, `pac90_test.go:246` |
| 3, 10, 12, 18 | unit | `backend/auth/` (new `_test.go` beside the verifier) |
| 15, 16 | guard | existing files, assertions untouched |

The fixture requirements are spec §4 "Fixtures":

- canonical GUIDs;
- one tenant spelled in upper-case hex;
- the consumer tenant only in AC-9;
- a fresh verifier wherever fetches are counted.

## 4. Alternatives rejected

1. **Update the `microsoftOAuthCallback` prose that describes the step-up
   verifier.** Step 2, `auth.yaml:461-464`, says the step-up verifier "reads
   the `/common` keys and rejects a `tid` only when it contains `/?#`
   (revision 3.1)". That sentence becomes historical once PAC-89 ships.
   Rewording it changes the bundle and the web's generated
   `schemas.types.d.ts:4484-4485`. That would force a paired web
   regeneration and a cross-repo merge pair for a comment. The sentence is
   about the sign-in operation's design history, and it is tagged as
   revision 3.1. It makes no promise about any HTTP behaviour, so no
   consumer is misled. Recorded as a follow-up for the next contract
   revision that touches `auth.yaml` (manifest `followUps`). Rejected for
   this change.
2. **Add a new denial reason (`personal_account`) for the consumer tenant.**
   This would be a new enum value on the outcome redirect, which means a
   contract change and new web copy for a token that the real `organizations`
   authority never issues. `tid_mismatch` is already accurate: the tenant is
   not an organisation with the domain verified. Rejected (spec D-4).
3. **Key the shared cache by tenant only, as sign-in does today.** It is
   simpler, but in a test-mode deployment a stand-in's keys for tenant T would
   then verify sign-in tokens for T. Rejected (spec D-2, AC-18).
4. **A migration that lower-cases `ms_tenant_id`.** Case-insensitive
   comparison at all three sites makes it unnecessary. A migration would also
   need its own down step and rollback note for no observable gain. Rejected
   (spec D-6, OQ-F1).
5. **Drop the Graph `/organization` check now that the token proves the
   tenant.** The token does not prove that the org domain is verified in that
   tenant. Rejected (spec §5, D-10).

## 5. Consumers

- `Enach/smart-calendar-flow`: none. No reason, status or field changes. The
  `tid_mismatch` copy (`SsoAdminStepupSection.tsx:33`,
  `OrganizationAISection.tsx:52`) is accurate for the consumer-tenant case.
- `clockwise-like` tests: the Microsoft step-up fixtures change (spec §6).
  Assertions do not.
- e2e: none. The e2e stack configures no Microsoft provider.
