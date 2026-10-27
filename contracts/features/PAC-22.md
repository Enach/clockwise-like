# Contract — PAC-22: SSO configuration must not disclose identity-provider secrets

- **Spec**: `docs/specs/PAC-22.md`, v3.2 (challenged 2026-10-27, accept-with-changes;
  every change applied in v3.2, see the spec's "Revision v3.2").
- **Linear**: https://linear.app/paceday/issue/PAC-22
- **Manifest**: `contracts/features/PAC-22.yaml`
- **Branch**: `factory/pac-22-contract`, cut from `origin/main` at `6d751f9`, with the
  spec branch `factory/pac-22-spec` (`4a517e2`) fast-forwarded in so that the
  contract and the spec it transcribes travel together.
- **Status**: contract revision 1, draft. Not yet challenged.
- **Author**: contract-author (stage 2). Nothing in this document was executed against
  a running server. Every claim about current behaviour is read from source on
  `6d751f9`; the gate results at the end are the only things that were run.

## 0. The base this contract is written on, and why it must be rebased

The spec orders PAC-50 before PAC-22 (D-5) and says that if PAC-22's contract is
drafted while PAC-50 is unmerged, it is drafted against PAC-50's contract and
counts only once rebased onto `main` after PAC-50 merges. On 2026-10-27 PAC-50 is
not merged (implementation PR #212 open). This revision is cut from `main`, as the
factory requires for every branch, and it reconciles the two orders like this:

- **Everything PAC-22 changes on existing operations is written against `main`.**
  The projection, the list container, the create-time validation and the
  preserved active state do not depend on who may write. The texts PAC-50 owns
  (who may call `createSsoProvider` and `deleteSsoProvider`, the domain rule,
  their 403 bodies, removal of nothing) are left exactly as they are on `main`
  and are labelled "unchanged by PAC-22; PAC-50 replaces this".
- **The new operation, `setSsoProviderState`, is specified against PAC-50's gate**,
  because D-6 forbids authorizing it by membership alone, and membership is the
  only rule `main` has. Its exact-domain rule and its `provider_domain_mismatch`
  400 are PAC-50's. Its description says in bold that it is not implementable
  before PAC-50 merges.
- Because `main` has no `SsoWriteForbiddenResponse`, its 403 references
  `ErrorResponse`, the same schema `createSsoProvider`'s 403 references on `main`.
  The rule the contract states is "the body createSsoProvider's 403 uses", and the
  `$ref` is the one place that has to follow PAC-50 on rebase.

### Rebase onto PAC-50 (revision 2 of this contract)

When PAC-50 merges, this branch is rebased onto the new `main` and becomes
revision 2. The rebase has known conflicts in `contracts/openapi/paths/auth.yaml`,
because both features rewrite `createSsoProvider`, `SsoProviderCreateRequest` and
`SsoProviderResponse`. They are resolved like this, and nothing else changes:

1. `createSsoProvider`: keep PAC-50's gate (steps 1 to 5 of its description, its
   400 `unknown property` / `provider_domain_mismatch`, its 403
   `SsoWriteForbiddenResponse`). Insert PAC-22's five validation checks into
   PAC-50's step 2 (body shape), in PAC-22's order, so that they still run before
   the authorization step. Replace PAC-50's paragraph "`enabled` is always forced
   to true … `provider_type: "saml"` … still accepted" with PAC-22's "Active
   state" and "Configurations stored before PAC-22" paragraphs. The 201 is
   `SsoProvider`.
2. `SsoProviderCreateRequest`: PAC-22's schema, keeping PAC-50's `domain`
   description (exact, case-insensitive, stored lower-cased) and its sentence on
   undeclared properties. PAC-22's sentence "Properties outside the schema other
   than the three SAML ones are ignored today" is deleted. The three SAML
   properties are still refused by name (`saml_cert is not supported`), not as
   `unknown property`, and that check still runs before PAC-50's undeclared-property
   check, so the named message wins.
3. `SsoProviderResponse`: deleted, as here. Its PAC-50 `Domain` note (lower-case
   since migration 028) moves to `SsoProvider.domain`.
4. `setSsoProviderState`: its 403 `$ref` becomes
   `#/components/schemas/SsoWriteForbiddenResponse`, and the sentence "Not
   implementable before PAC-50 merges" is replaced by "Authorized as
   createSsoProvider (PAC-50)".
5. `listSsoProviders`: PAC-50 does not touch it, so nothing conflicts.
6. `make openapi`, then every gate again; the manifest's `contractRevision`,
   `contractHash` and `gates` are rewritten for the rebased commit. Only that
   commit's gates count (spec D-5).

If PAC-50's merged contract differs from `origin/impl/pac-50-sso-write-gate-backend`
as read on 2026-10-27, the rebase follows what merged and says so in revision 2.

## 1. What changes at the HTTP boundary

| Operation | Change |
|---|---|
| `listSsoProviders` `GET /api/admin/sso` | 200 is `SsoProviderList` `{providers: SsoProvider[]}` instead of a bare array of the storage row. No secret under any name. Suspended configurations are listed with `state: suspended`. |
| `createSsoProvider` `POST /api/admin/sso` | 201 is `SsoProvider`. Body is a complete OIDC configuration: all six properties required and non-empty, `provider_type` is `oidc` only, the three SAML properties are refused by name. A replace keeps the stored active state; a create starts active. |
| `setSsoProviderState` `PUT /api/admin/sso/{domain}/state` | **New.** Body `{state: active \| suspended}` and nothing else. 200 `SsoProvider`; 400; 401; 403; 404; 500. |
| `startSsoLogin` `GET /api/auth/sso/{domain}` | Description only: the 404 also covers a suspended configuration (already true: the lookup filters on `enabled`); the 501 and the empty-issuer 500 are reachable only for rows stored before PAC-22. |
| `detectAuthProvider` `POST /api/auth/detect` | Description only (`SsoDetectResponse.type`): a suspended configuration is ignored (already true, same lookup). |
| `deleteSsoProvider` `DELETE /api/admin/sso/{domain}` | Unchanged. It already acts on any row, suspended or not, SAML or not (AC-8). |

Schemas: `SsoProviderResponse` is **removed**. `SsoProvider`, `SsoProviderList`,
`SsoProviderState` and `SsoProviderStateRequest` are **new**.
`SsoProviderCreateRequest` is **rewritten**.

## 2. Why the interface is shaped this way

### 2.1 The response is a projection with its own keys, not the row with fields removed

`SsoProvider` is a wire type with explicit snake_case keys. The storage row is never
encoded again. Two things follow:

- **The secret cannot come back by accident.** If the implementation added json
  tags to `storage.SSOProvider` and `json:"-"` on the two secrets, a later field
  added to the row would appear on the wire by default. A separate type with
  `additionalProperties: false` makes every property on the wire a decision, and
  the contract tests that validate responses against it fail on anything extra.
- **Key casing changes from PascalCase to snake_case.** The PascalCase keys were an
  accident (no json tags, U-22). The request body already uses snake_case
  (`provider_name`, `oidc_issuer`), and the response now uses the same names for the
  same values. There is no consumer to break (§4).

### 2.2 "A secret is on file" is two booleans, one per secret

AC-3 asks for one named property per secret that says only whether it is on file.
`has_oidc_client_secret` and `has_saml_cert` are each computed from their own
column being non-empty, so each changes only when its own secret does (AC-4).

**Rejected: a masked value** (`"oidc_client_secret": "****abcd"`, the pattern many
admin consoles use). It keeps a property named after the secret on the wire, which
AC-1 forbids ("no property … carries either one under any name"), and the last
four characters are a fragment of the secret, which §3.1 says may not leave the
server. It also invites a later "reveal" feature that §6 forbids.

**Rejected: one `secrets_on_file: ["oidc_client_secret"]` array.** It is one
property for two facts, so a reader has to search it, and AC-3 asks for a named
property per secret.

### 2.3 Active state is an enum named `state`, not the `enabled` boolean

The spec talks about a configuration being *active* or *suspended*, and the
transition "asks for a state". `SsoProviderState` is `enum [active, suspended]`,
shared by the response and the transition's request, so the value a caller sends
is the value it reads back.

**Rejected: `enabled: boolean`**, the storage column's name. A boolean invites a
toggle, which §3.3 rules out, and "enabled" says nothing about the configuration
being kept while it is off. The enum also leaves room for a third state without a
breaking change of type. The column stays `enabled`; the mapping (true is
`active`) is stated on the schema, and needs no migration (§6 of the spec).

### 2.4 The transition is `PUT /api/admin/sso/{domain}/state`

The transition is a separate operation (§3.3: the spec requires it outright). Its
shape follows from the spec's constraints:

- **It names its target the way removal does**: the same `{domain}` segment as
  `deleteSsoProvider`, under the same prefix (AC-12).
- **It states the outcome, it does not flip** (§3.3): the body is the desired
  state, and `PUT` is the method whose meaning is "make it so". Repeating it is
  harmless, which is exactly AC-20's "already in the requested state succeeds and
  changes nothing".
- **It accepts nothing else** (AC-12): `SsoProviderStateRequest` has one property
  and `additionalProperties: false`, and the server refuses any other property with
  `unknown property: <name>` itself rather than relying on PAC-50's decoder change.
  This is the structural guarantee AC-10 rests on: there is no request this
  operation accepts that can carry a secret.

**Rejected: `PATCH /api/admin/sso/{domain}` with `{state}`.** A PATCH on the
configuration resource reads as a partial update of the configuration. The next
feature that needs to change one configuration value would add it to the same
PATCH body, and suspension would be folded back into a configuration write, which
is the exact regression AC-12 is written to catch. It would also put a merge
operation beside a replace operation on the same resource, which §3.2 declines.

**Rejected: `POST …/{domain}/suspend` and `POST …/{domain}/resume`.** Two
operations with identical gates, error paths and recording, differing in one
value. Every rule in §3.3 would be stated twice and could drift. The spec's "asks
for a state" reads naturally as one operation with the state as data.

**Rejected: `PUT /api/admin/sso/{domain}/enabled` with a bare JSON boolean body.**
A bare boolean is the toggle shape again, and a non-object body cannot grow.

### 2.5 The error paths of the transition

The order of checks is fixed so that every refusal decided from the request alone
(membership, body, domain) comes before the authorization is spent, and every
outcome that needs the stored row (not found, unchanged, changed) comes after
(§3.3, D-9). This is PAC-50's ordering for its own writes, extended:

- 404 `sso provider not found` is PAC-50's message for a removal that finds
  nothing, reused so that the same condition has the same text.
- 400 `provider_domain_mismatch` is PAC-50's exact-domain refusal. The transition
  uses the exact rule even though `main`'s create still uses the suffix rule,
  because AC-21 requires a subdomain to be refused, and a new operation has no
  suffix-rule behaviour to preserve.
- An unchanged transition answers 200 with the stored configuration, not 409:
  D-9 explains why "it is off" is the true answer to "turn it off".

**`updated_at` is not touched by the transition.** AC-10 says every described value
other than the active state is unchanged after suspend and resume, and AC-20 says
the same of a no-op. `updated_at` is a described value, so the contract defines it
as "the time of the last configuration write". A separate "state changed at"
timestamp was considered and not added: no criterion needs it, and PAC-50's event
record already holds who changed the state and when.

### 2.6 The create body describes only what can work

`SsoProviderCreateRequest` now describes a complete OIDC configuration and nothing
else: six required, non-empty properties, `provider_type: enum [oidc]`, and no SAML
properties. A generated client cannot express a SAML or incomplete submission at
all, which is the convention "prefer making the wrong request impossible to
express". The server still refuses each one with a named message, because a
schema does not stop a hand-written request.

**Rejected: keep `saml` in the enum and add `oneOf`/`dependentRequired` per
provider type** (the audit's suggestion for U-21, `api-audit.md:489`). It would
describe SAML as a valid submission, which D-1 says it no longer is, and it would
put the SAML certificate back into a request schema that generated clients then
offer to fill in.

**Rejected: drop the SAML properties silently** (accept and ignore them). D-8
refuses that: the caller would be told something they supplied was accepted when
it was thrown away.

**The SAML properties are refused by name, before every other check except JSON
parsing.** A body that sets `provider_type: saml` and `saml_cert` is told about
`saml_cert` first, so the caller learns that a certificate will not be accepted
even once the type is corrected. The order of all ten 400 messages is fixed in the
contract so that AC-7 and AC-18 can assert one message per case.

**`provider_type 'saml' is not supported` is a new message, and `provider_type
must be 'oidc' or 'saml'` becomes `provider_type must be 'oidc'`.** The old text
would now invite a value that is refused. These are the only existing messages
that change; `domain, provider_name and provider_type are required` is kept as it
is, although per-property messages would be more uniform, because the spec does
not ask for that change.

**Values are not trimmed, and whitespace-only values are accepted.** D-2 says
"non-empty", and the contract encodes exactly that (`minLength: 1`). An issuer of
`" "` cannot work, but neither can a well-formed issuer that points nowhere; the
server cannot decide "works" at submission. Refusing whitespace (`pattern: '\S'`)
or requiring `format: uri` on the issuer was considered and not done, because
neither is in the spec and each would be a behaviour this stage chose alone.

### 2.7 The list is a named container

`SsoProviderList` wraps the array in `providers` (§3.7). The reason is only to
allow later additions without changing the response type for every consumer. The
schema's description repeats §3.7's instruction that it must not be "corrected"
towards the bare arrays most other list operations return.

**Rejected: keep the bare array**, which would be consistent with most of the
contract. §3.7 and AC-6 require the container.

### 2.8 The non-secret SAML values stay in the response

`saml_entry_point` and `saml_issuer` are kept on `SsoProvider`, and `has_saml_cert`
reports the certificate. Only rows stored before PAC-22 can hold them, and §3.1
says those rows stay readable and a reader must still be able to see what they
hold. They are addresses, not secrets.

**Rejected: drop every SAML property from the response.** A pre-existing SAML row
would then list as a configuration of type `saml` with nothing in it, and an
operator could not tell what to remove.

## 3. What this contract does not change

- **Who may call any existing operation.** `createSsoProvider`'s and
  `deleteSsoProvider`'s membership, domain and 403 texts are `main`'s, untouched.
  PAC-50 owns them (spec §5).
- **`deleteSsoProvider`** in any way, including its 204 for a missing row
  (API-067 is PAC-50's).
- **Who may read.** `listSsoProviders` stays open to any member (spec §5). It now
  discloses nothing secret.
- **Stored data.** No migration. The secret columns keep their values. The
  transition writes the existing `enabled` column.
- **Sign-in.** `startSsoLogin` and `ssoOidcCallback` behave exactly as before; the
  descriptions only say what a suspended row already does.

## 4. Consumers

Searched on 2026-10-27 in both repos (`grep -rn "admin/sso\|SsoProvider"`):

| Consumer | Breaks? | Detail |
|---|---|---|
| `smart-calendar-flow/src/api/generated/{types.ts,schemas.ts,schemas.types.d.ts}` | Regenerated, not broken | The only references. `SsoProviderResponse` disappears from them. Nothing outside `generated/` imports it, and no page calls `/api/admin/sso` (spec §2.7). |
| `clockwise-like/backend/api/gen` | Regenerated | The handlers do not use the generated types for these operations (all `handwritten`, `MIGRATION.md`); the new operation is to use the generated server interface (factory §4). |
| `clockwise-like/backend/storage/sso_providers_test.go:59` `TestUpsertSSOProvider_Update` | Changes for one property only | Asserts that the upsert replaces every column, `enabled` included. D-3 takes `enabled` out of the configuration write; the plan names that one assertion (spec §6). |
| `clockwise-like/backend/api/handlers_sso_test.go`, `pac35_auth_flows_test.go` | No | Seed rows through storage and drive sign-in; sign-in is unchanged (AC-14). |
| PAC-50 `TestSSOWrite_OneGrantOneWrite` (unmerged, PR #212) | Yes, deliberately | Pins the thirteen PascalCase keys of the 201. PAC-22's implementation rewrites it to the `SsoProvider` shape (spec §2.8, factory §7 rule 2). |
| `clockwise-like/mcp`, `e2e` | No | Neither references the admin SSO operations. |
| Outside the repositories | None | Production has zero users (spec D-4). |

## 5. Findings and markers

| ID | Resolution in this contract |
|---|---|
| API-001 (disclosure half) | Resolved: no response of any operation carries either secret. The authorization half is PAC-50's. The audit row is marked Fixed by the implementer when PAC-22 merges (spec D-7), not by this stage. |
| API-060 | Resolved: SAML and incomplete OIDC bodies are refused at submission with named 400s. |
| API-066 | Resolved: `setSsoProviderState`; the configuration write preserves the state. |
| API-092 (`listSsoProviders` share) | Not a contract change. The 200 and 403 assertions are the implementation's (spec D-7). |
| U-21 (`SsoProviderCreateRequest`) | Resolved by decision: spec D-1, D-2, D-8 decide what is validated; the schema encodes it and the marker is removed. PAC-50 also removes this marker on its branch; on rebase the PAC-22 text wins. |
| U-22 (`SsoProviderResponse`) | Resolved by removal: the schema is replaced by `SsoProvider`, whose keys are defined by the contract rather than inferred from a missing json tag. The implementation's contract test observes them. |
| API-067, API-091 | Not touched (PAC-50; spec §5). |

No operation this feature touches carries an `x-uncertain` marker after this
revision. The bundle's count went from 12 markers to 10 (assembler summary).

## 6. Rollback

No migration, no stored data change. Reverting the contract reverts the generated
types with it. A revert of the implementation brings the leak back (spec §7), and a
configuration suspended after release would be silently resumed by the first
pre-release configuration write; the spec's rollback section says what to do
before reverting.

## 7. Gates

Recorded in `contracts/features/PAC-22.yaml` (`gates`), with the commands run and
their output.
