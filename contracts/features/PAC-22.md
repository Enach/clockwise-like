# Contract — PAC-22: SSO configuration must not disclose identity-provider secrets

- **Spec**: `docs/specs/PAC-22.md`, v3.4 (the challenge of v3.3, 2026-10-28,
  accepted it with changes; v3.4 applies every one, see the spec's "Revision
  v3.4"). Revisions 1 and 1.1 of this contract were written against v3.2.
- **Linear**: https://linear.app/paceday/issue/PAC-22
- **Manifest**: `contracts/features/PAC-22.yaml`
- **Branch**: `factory/pac-22-contract`, cut from `origin/main` at `6d751f9`, with the
  spec branch `factory/pac-22-spec` (`4a517e2`) fast-forwarded in so that the
  contract and the spec it transcribes travel together.
- **Status**: contract revision 1.2, draft, awaiting contract-challenger.
  Revision 1 (`79d63d9`) was challenged on 2026-10-27 (accept-with-changes, three
  blocking); revision 1.1 applied that challenge. Revision 1.2 (2026-10-28)
  transcribes spec v3.4: an omitted client secret is kept on a replace, bound to
  the issuer and client id, decided at the moment of storing (see "Revision 1.2"
  below). Revision 2 is still the rebase onto PAC-50 (§0), and only its gates
  count for implementation.
- **Author**: contract-author (stage 2). Nothing in this document was executed against
  a running server. Every claim about current behaviour is read from source on
  `6d751f9` (revisions 1, 1.1) and re-checked on `origin/main` `69ffa92`
  (revision 1.2: `git diff 6d751f9 69ffa92` on `backend/api/handlers_sso.go`,
  `backend/storage/sso_providers.go` and `backend/domain/domain.go` is empty);
  the gate results in the manifest are the only things that were run.

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
  refusal are PAC-50's; its status is 403, the owner's decision for PAC-50 on
  2026-10-27 (spec D-15; revision 1.2, was 400). Its description says in bold that it is not implementable
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
   the authorization step. PAC-22's check 6 (an omitted client secret, against
   the stored configuration: revision 1.2) goes after PAC-50's domain check and
   before its authorization step; the store-time re-check (spec D-14) goes into
   PAC-50's recorded upsert, inside its transaction. Replace PAC-50's paragraph "`enabled` is always forced
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
| `createSsoProvider` `POST /api/admin/sso` | 201 is `SsoProvider`. Body is an OIDC configuration: `domain`, `provider_name`, `provider_type`, `oidc_issuer`, `oidc_client_id` required and non-empty, `provider_type` is `oidc` only, the three SAML properties are refused by name. `oidc_client_secret` is optional (revision 1.2): supplied it must be non-empty and replaces the stored one; left out of a replace it is kept only when the stored row is `oidc`, holds a non-empty secret and has exactly the submitted issuer and client id, decided again when the write is stored; otherwise 400. A replace keeps the stored active state; a create starts active. |
| `setSsoProviderState` `PUT /api/admin/sso/{domain}/state` | **New.** Body `{state: active \| suspended}` and nothing else. 200 `SsoProvider`; 400; 401; 403 (including `provider_domain_mismatch`, revision 1.2); 404; 500. |
| `startSsoLogin` `GET /api/auth/sso/{domain}` | Description only: the 404 also covers a suspended configuration (already true: the lookup filters on `enabled`); the 501 is reachable only for rows stored before PAC-22. The 500 (issuer discovery failed) stays reachable for any row whose discovery fails, including a row written since PAC-22 whose issuer is unreachable or whitespace. |
| `ssoOidcCallback` `GET /api/auth/callback/oidc/{domain}` | Description only: the 404 also covers a suspended configuration, so a sign-in started before a suspension and finished after it gets the 404 and no session (already true: same lookup, `handlers_sso.go:112-116`). |
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
- 403 `provider_domain_mismatch` is PAC-50's exact-domain refusal, with the
  status the owner chose for it on 2026-10-27 (spec D-15; revisions 1 and 1.1
  said 400, copied from PAC-50's branch, which still says 400 at `51aa70a`).
  The transition uses the exact rule even though `main`'s create still uses the
  suffix rule, because AC-21 requires a subdomain to be refused, and a new
  operation has no suffix-rule behaviour to preserve. It stays in step 3, before
  the authorization, so it spends nothing.
- An unchanged transition answers 200 with the stored configuration, not 409:
  D-9 explains why "it is off" is the true answer to "turn it off".
- A body that gives `state` twice is refused, 400 `duplicate property: state`
  (revision 1.1). Go's decoder would keep the last value, and an author of
  `{"state":"active","state":"suspended"}` asked for two things. For a kill
  switch, the conservative answer to an ambiguous request is to do nothing.
  The check sits in step 2 with the unknown-property check, in body order, so
  it is decided from the request alone and spends no authorization.
  **Rejected: "last one wins"** (what a plain decode does): it makes the
  outcome depend on the decoder rather than on the contract.
- The state write and its `sso_provider_written` record are one transaction
  (revision 1.1), as PAC-50's `UpsertSSOProviderRecorded` and
  `DeleteSSOProviderRecorded` already are on its branch. After a 500 the state
  is the one stored before the request and nothing is recorded; an
  authorization consumed in step 4 stays consumed, as PAC-50 states for its own
  500s. **Rejected: write, then record outside the transaction**: a 500 between
  the two would leave a state change nobody can attribute, which is what AC-16
  exists to prevent.

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

**The list holds at most one item today** (revision 1.1). `sso_providers.domain`
is unique (`008_sso_providers.up.sql:3`), `organizations.domain` is unique
(`007_organizations.up.sql:4`), and the list joins them exactly
(`backend/storage/sso_providers.go:89`). So an org has at most one listed
configuration, and the `providers` description says so. The array stays an
array: the type must not change if an org can ever hold more than one, and
narrowing it to a single object would be the breaking change the container was
introduced to avoid. AC-6 is transcribed accordingly: zero and one item through
the API, and the multi-item shape by validating a constructed `SsoProviderList`
with two `SsoProvider` items against the schema (manifest, AC-6).

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

### 2.9 An omitted client secret is kept, but only where it would be sent (revision 1.2)

Spec v3.4 §3.2 (D-12, D-13) makes the client secret the one value a replace may
leave out. The contract encodes it like this:

- `oidc_client_secret` leaves `required` and keeps `minLength: 1` with no
  `nullable`. So a generated client can omit it, but cannot express `""` or
  `null`; the server still refuses both with `oidc_client_secret must not be
  empty` (AC-26), because a schema does not stop a hand-written request.
- **Absence is decided against the stored row, in a new check 6.** Checks 1-5
  stay request-only. Check 6 runs only when the secret is absent, after the
  domain match (so it only ever reads the caller's own organisation's row) and
  before any PAC-50 authorization (so AC-17 holds). It has two messages:
  `oidc_client_secret is required` (no row, a row that is not `oidc`, or a row
  whose secret is empty: AC-7, AC-25) and `oidc_client_secret is required when
  oidc_issuer or oidc_client_id changes` (AC-24). Both name the secret, as the
  spec asks; the second tells the person why a value they did not touch is
  suddenly needed.
- **The comparison is byte-for-byte** (D-13: no case folding, no trailing-slash
  normalisation), and is stated on the operation so the implementer does not
  "helpfully" normalise.

What check 6 lets a caller learn is nothing new: whether a configuration exists
for their own domain and what its issuer and client id are, which any member
already reads from `listSsoProviders`.

**Rejected: keep revision 1.1's pure replace** (secret required on every write).
Spec v3.4 D-12 reverses it: a write-only value under a pure replace is erased or
must be re-entered by every edit (PAC-24 §0's argument for `llmApiKey`).

**Rejected: a sentinel value** (`"oidc_client_secret": "__unchanged__"` or a
masked echo such as `"****"`). It puts a property named after the secret back
into a shape a client round-trips, invites clients to send back whatever the
server gave them, and makes one string value mean "do not store me". Absence is
the conventional and unambiguous signal, and it is what PAC-24 uses.

**Rejected: a separate operation to rotate the secret** (`PUT
/api/admin/sso/{domain}/secret`). It would make "change the issuer and the
secret together" two writes, with a window in which the new issuer holds the old
secret: exactly the redirect D-13 forbids. Under PAC-50 it would also cost two
step-ups.

**Rejected: keep the secret whatever else changes.** That is the redirect D-13
closes (spec §2.2): point the issuer at a server you control, leave the secret
out, start a sign-in.

**Rejected: `""` clears the secret**, as PAC-24 lets an empty `llmApiKey` clear
the key. Here an empty secret is a configuration that cannot sign in (D-2), and
the spec says no write clears it (D-12); `deleteSsoProvider` is the way.

### 2.10 What a replace keeps is decided when it is stored (revision 1.2)

Spec D-14: between a replace's checks and its store another write can land
(PAC-50 spends its authorization between the two). The contract therefore says
that what a replace keeps is decided **again, atomically with the store**,
against the row as it is then:

- an omitted secret is kept only if check 6's conditions still hold; if they do
  not, the request is refused with the message check 6 would give against that
  row, and nothing is stored (AC-27). No row is ever created by a replace that
  found its row removed;
- the active state is never written by a replace, so a transition stored in the
  gap survives (AC-28); and `setSsoProviderState` writes only the active state,
  so a replace stored in its gap keeps its values.

How that atomicity is achieved (a conditional upsert, a row lock in PAC-50's
transaction) is the plan's business, and the contract does not name it. Whether
an authorization is spent on a refusal only the store can detect is left open,
as D-14 leaves it.

**Rejected: decide only at check time** (revision 1.1 had nothing to decide, so
this is the naive implementation of v3.3). It lets an administrator's issuer and
secret change be followed by a stale replace that stores its issuer with the
administrator's secret, and lets a stale replace resume a suspended SSO.

**Rejected: a version or ETag precondition** (`If-Match`). It would make every
client do a read-modify-write and handle 412, for a race that needs two
permitted callers writing the same configuration at once; the spec fixes the
outcome without it, and adding a precondition is behaviour the spec does not ask
for.

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
| `clockwise-like/backend/storage/sso_providers_test.go:59` `TestUpsertSSOProvider_Update` | No | Asserts only `ProviderName == "Okta"` after a replace (`:84`), and sets `Enabled: true` on both writes, so D-3 does not break it. It therefore guards neither property this contract relies on. The plan must name new assertions: (a) a replace of a row stored with `enabled = false` leaves it `false`, and of a row with `enabled = true` leaves it `true`; (b) a replace replaces every other configuration column (`provider_name`, `provider_type`, `oidc_issuer`, `oidc_client_id`, `oidc_client_secret`, `saml_entry_point`, `saml_issuer`, `saml_cert`), including a column cleared to `""` (a SAML value and the secret), and sets `updated_at`. (Revision 1 said this test asserts every column; it does not.) |
| `clockwise-like/backend/api/handlers_sso_test.go`, `pac35_auth_flows_test.go` | No | Seed rows through storage and drive sign-in; sign-in is unchanged (AC-14). |
| PAC-50 `TestSSOWrite_OneGrantOneWrite` (unmerged, PR #212) | Yes, deliberately | Pins the thirteen PascalCase keys of the 201. PAC-22's implementation rewrites it to the `SsoProvider` shape (spec §2.8, factory §7 rule 2). PAC-50's `pac50_sso_gate_test.go:152` pins `OIDCClientSecret` and `SAMLCert` likewise (spec v3.4 Revision, non-blocking 7). |
| `smart-calendar-flow` PR #120 (`factory/pac-22-contract-types`) | Stale until regenerated | Its types were generated from revision 1.1, which required `oidc_client_secret`. Revision 1.2 regenerates the same branch with `REGEN=1 web-gate` (manifest `gates.webGate`); the PR is updated in place rather than replaced. |
| `clockwise-like/backend/storage/sso_providers.go` `UpsertSSOProvider` | Behaviour to change (implementation) | Its `ON CONFLICT` overwrites `enabled` and `oidc_client_secret` from `EXCLUDED` (`:63-73`). The implementation must stop writing `enabled` on conflict and keep the stored secret on an omitted one, under the store-time conditions of §2.10. |
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

## Challenge — 2026-10-27

Applies to contract revision 1, commit `79d63d9` on `factory/pac-22-contract`
(bundle sha256 `46b978ed…8fd9`, identical in `d4a2389` and `79d63d9`). Read
against the committed artefacts, the handler and storage on this branch
(unchanged from `main` `6d751f9`), spec v3.2, and PAC-50's contract on
`origin/impl/pac-50-sso-write-gate-backend` at `51aa70a`. The challenger did not
see the author's reasoning beyond this document and the manifest.

**Verdict**: accept-with-changes

**Checks run by the challenger** (nothing else is claimed):

- `make openapi-check WEB=/nonexistent` in an LF clone of this branch
  (golang:1.25-bookworm): `OK: bundle is in sync (99 paths)`, `OK: MIGRATION.md
  covers all 120 operations (0 generated)`, `OK: backend/api/gen matches the
  contract`, `SKIPPED: web generated types (no repo at /nonexistent)`; rc=0.
  `go build ./...` in `backend/` in the same clone: rc=0.
- `python3 scripts/openapi_assemble.py --check` in the worktree: `OK: bundle is
  in sync (99 paths)`.
- `x-uncertain:` keys in the bundle: 12 on `6d751f9`, 10 here. Matches the
  manifest.
- `sha256sum` of the bundle at `d4a2389` and `HEAD`: both `46b978ed…8fd9`.
  Matches `contractHash`.
- Not run: the full api-gate (the host checkout is on another branch and was
  not switched), the web half of openapi-check, e2e.

### Blocking

1. **createSsoProvider says PAC-22's checks run before the membership check.
   The handler, PAC-50 and this contract's own rebase plan put membership
   first.** `auth.yaml` (createSsoProvider description) says: "The caller must
   belong to an org and the supplied domain must match … PAC-22 neither widens
   nor narrows it, and its own checks below all run before it." Here "it" is
   the whole rule, membership included.
   - Request: a user with no org (`users.org_id` NULL) sends `POST
     /api/admin/sso` with `{"domain":"acme.com","provider_name":"x","provider_type":"saml"}`.
   - Read literally, the contract answers 400 `provider_type 'saml' is not
     supported`.
   - The handler answers 403 `org membership required` before it decodes the
     body (`backend/api/handlers_sso.go:167-172`).
   - PAC-50 makes membership step 1 and body shape step 2. PAC-22.md §0, rebase
     item 1, inserts PAC-22's checks "into PAC-50's step 2".
   - `setSsoProviderState` in this same contract also checks membership first
     (step 1).

   So the contract states an order the code contradicts and that its own
   revision 2 would reverse. An implementer working from revision 1 would move
   the membership check. **Fix**: reword the sentence to: "Membership (403
   `org membership required`) is checked first, as today. Checks 1-5 below run
   after it and before the org lookup, the domain match and any PAC-50 write
   authorization."

2. **The manifest's AC-6 asks for an org with two configurations. No such org
   can exist.**
   - `sso_providers.domain` is `UNIQUE` (`008_sso_providers.up.sql:3`).
   - `organizations.domain` is `UNIQUE` (`007_organizations.up.sql:4`).
   - The list query joins `o.domain = s.domain` exactly
     (`backend/storage/sso_providers.go:89`), and the contract repeats that
     ("listed when its domain equals the org's domain exactly").

   So `listSsoProviders` returns at most one item for any org. A second row for
   a subdomain or for a different casing is never joined. "Org with 0, 1, then
   2 configurations: … validates each time" is therefore a test that cannot be
   arranged. Spec AC-6 says "more than one" too, and the contract stage
   inherited it silently. **Fix** (manifest and `SsoProviderList.providers`
   description):
   - say that the list holds at most one item today, and why;
   - change AC-6 to exercise 0 and 1 through the API;
   - cover the multi-item shape only by validating a constructed
     `SsoProviderList` against the schema, or send AC-6 back to the spec. Do not
     leave a criterion that cannot be satisfied.

3. **The consumer row for `TestUpsertSSOProvider_Update` misdescribes the
   test.**
   - PAC-22.md §4 says the test "Asserts that the upsert replaces every column,
     `enabled` included". The manifest says it "breaks: one assertion".
   - The test (`backend/storage/sso_providers_test.go:59-87`) sets `Enabled:
     true` on both writes and asserts only `ProviderName == "Okta"`. It asserts
     nothing about `enabled` and nothing about any other column.

   So it does not break. Two properties that the contract and spec §6 rely on
   are not guarded by it: that the replace still replaces every configuration
   value, and that a replace now keeps `enabled`. **Fix**: correct the row in
   both files to say "does not break; asserts `provider_name` only". The plan
   must then name new assertions:
   - a replace keeps `enabled = false` and keeps `enabled = true`;
   - every other configuration column is replaced, including the SAML columns
     and the secret being cleared.

### Requests wrongly permitted

None found beyond Blocking 1, which is about order, not about what is
admitted. Attacked:

- `POST /api/admin/sso` with an otherwise complete OIDC body plus
  `"saml_cert": null` → step 2, `saml_cert is not supported`. The schema has no
  such property, so a generated client cannot send it.
- `{"provider_type":"saml","saml_cert":"x", …}` → `saml_cert` is named first, as
  §2.6 intends.
- `"oidc_client_secret": null` → `oidc_client_secret is required`.
- `"oidc_issuer": " "` → accepted. This is documented as deliberate (§2.6) and
  its consequence is stated.
- `PUT /api/admin/sso/acme.com/state` with
  `{"state":"suspended","oidc_client_secret":"x"}` → 400 `unknown property`.
- `{"state":"Suspended"}` and `{}` → 400 `state must be …`.
- `PUT /api/admin/sso/sub.acme.com/state` → 400 `provider_domain_mismatch`.
- `PUT /api/admin/sso/stepup/state` → 400 `provider_domain_mismatch`.

### Responses the client cannot handle

None. No hand-written consumer exists. On `origin/main` of smart-calendar-flow,
`SsoProvider`/`admin/sso` appear only in `src/api/generated/{types.ts,
schemas.ts, schemas.types.d.ts}`.

The projection was checked against the table. Every column is `NOT NULL`
(`008_sso_providers.up.sql:2-14`), so every required string and boolean of
`SsoProvider` is always derivable. `provider_type`'s CHECK matches the enum
`[oidc, saml]`, and the list's `ORDER BY s.created_at` matches "oldest first".

### Consumers affected

- smart-calendar-flow `src/api/generated/*`: regenerated. `SsoProviderResponse`
  disappears and nothing imports it (verified on `origin/main`).
- clockwise-like `backend/api/gen`: regenerated in `d4a2389`, in sync, builds.
- `mcp/`, `e2e/`: no reference to `/api/admin/sso` (grep, this branch).
- `backend/api/pac21_contract_5xx_test.go` reads the bundle. The new 500 uses
  `ErrorResponse` with the PAC-21 wording, so it is consistent with what that
  test checks. Not run here.
- `backend/storage/sso_providers_test.go:59`: see Blocking 3.

### Contract/implementation divergences

- Blocking 1.
- Pre-existing, re-stated rather than introduced: `SsoProviderCreateRequest`
  has `additionalProperties: false`, but the handler ignores undeclared
  properties (`handlers_sso.go:174-185`, a plain `Decode`). The description now
  says so ("ignored today"), and PAC-50 closes it on rebase. Acceptable for
  revision 1 because the divergence is stated.

### Uncertainties not actually resolved

None. U-21 is decided by spec D-1, D-2 and D-8, and each decision is pinned by
AC-7, AC-8 and AC-18. U-22's question (casing inferred, not observed) dissolves
because the wire type is now defined by the contract, and AC-5 and AC-6
validate responses against it. Both removals carry evidence and a test.

### Verified clean

- **Suspension semantics.** `GetSSOProviderByDomain` filters `enabled = true`
  (`sso_providers.go:51`). Detection (`backend/auth/sso_detect.go:37`), start
  (`handlers_sso.go:51`) and callback (`:112`) all go through it, so the
  described 404 and "not `sso`" for a suspended row is what the code does.
- **deleteSsoProvider** does not filter on `enabled` (`sso_providers.go:110`),
  as §1 says.
- **`updated_at` untouched by the transition is implementable.** There is no
  trigger on `sso_providers`; only the upsert sets `updated_at = NOW()`
  (`sso_providers.go:73`).
- **Rebase $ref.** PAC-50's `SsoWriteForbiddenResponse` is a `oneOf` whose
  `SsoWriteMembershipRequired` variant carries `error: [org membership
  required, org not found]` (`51aa70a`). Swapping setSsoProviderState's 403
  `$ref` in revision 2 therefore also covers its step-1 403s.
- **PAC-50's step 1 is membership, including `org not found`.** This matches
  setSsoProviderState's step 1.
- **Routing.** `PUT /api/admin/sso/{domain}/state` is one segment deeper than
  `DELETE /api/admin/sso/{domain}` and does not collide. The `stepup` case is
  stated.
- **Acceptance tests.** The manifest's `acceptanceTests` cover every spec
  criterion except AC-13, which the spec withdrew. Only AC-6 diverges
  (Blocking 2).
- **Security conventions.** The new operation inherits global `bearerAuth` (no
  `security: []`). No response schema has a property carrying either secret,
  and both `SsoProvider` and `SsoProviderList` are `additionalProperties:
  false`.
- **Rejected alternatives** (§2.2-§2.4, §2.6): each is rejected for a reason
  tied to a criterion (AC-1, AC-3, AC-12, D-1, D-8), not to taste.

### Non-blocking

1. **ssoOidcCallback's 404 is not updated.** A sign-in that started before a
   suspension reaches the callback after it and gets 404 `SSO not configured
   for this domain` (`handlers_sso.go:112-116`). The callback's 404
   description should say "no active row, none or suspended (PAC-22)", as
   startSsoLogin's now does.
2. **The "empty-issuer 500" wording can be misread.** startSsoLogin's 500 is
   "Issuer discovery failed", which stays reachable for any row written after
   PAC-22 whose issuer is unreachable or whitespace (`" "` passes
   `minLength: 1`). The manifest's summary "501 and empty-issuer 500 reachable
   only for rows stored before PAC-22" reads as if the 500 were. Suggest:
   "the 500 stays reachable for any row whose discovery fails".
3. **Atomicity is not stated for setSsoProviderState.** The contract does not
   say whether the state write and the `sso_provider_written` record are one
   transaction, so a 500 after the write leaves "did the state change?"
   undefined. The 500 description, or the plan, should say which.
4. **The `MIGRATION.md` register disagrees with §4.** The register lists
   `setSsoProviderState` as `handwritten`, while PAC-22.md §4 says it is to use
   the generated server interface. The implementation must flip the row.
5. **Factory §2 gate 4 (web types in sync) is not met on revision 1**, as the
   manifest admits. Revision 2 must ship with the paired web PR (`REGEN=1
   web-gate`) green before implementation starts.
6. **Duplicate `state` keys are undefined.** For
   `{"state":"active","state":"suspended"}`, Go keeps the last value, and the
   contract does not say which wins. This is low risk, but it is unspecified.
7. **OWNER:** OQ-5 (fixing and resuming a configuration takes two writes, two
   step-ups).
8. **OWNER:** OQ-6 (no OIDC public clients).
9. **OWNER:** OQ-7 (the kill switch may need the operator).
10. **OWNER:** a subdomain row stored under `main`'s suffix rule cannot be
    reached through the API once PAC-50's exact rule lands. An example is
    `sub.acme.com` written by an `acme.com` member before PAC-50. Such a row:
    - stays active for sign-in (detection looks up the email's own domain);
    - is not listed (the join is exact);
    - cannot be suspended (AC-21) or deleted (PAC-50's exact rule).

    Only the operator can turn it off. Production has zero users, so none exists
    there (D-4), but other deployments may have such rows. Is "operator only" the
    accepted answer?

## Revision 1.1 — 2026-10-27

This revision answers the challenge above (applies to revision 1, `79d63d9`). The
challenge accepted revision 1 with changes. Every change it lists is applied, in
the challenge's order. The contract content is commit `7dfe2bd`, the regenerated
Go code `9f599f3`; bundle sha256 `a85c9e99…c412`. PAC-50 is still unmerged
(PR #212 open), so this is not revision 2: the rebase in §0 is still owed, and
only the rebased commit's gates count for implementation (spec D-5).

**Blocking**

1. **Membership comes first on `createSsoProvider`.** The sentence "its own checks
   below all run before it" is gone. The description now says: membership (403
   `org membership required`) is checked first, before the body is read, as the
   handler does today (`handlers_sso.go:167-172`); checks 1-5 run after it and
   before the org lookup (403 `org not found`), the domain match (403 `can only
   configure SSO for your own domain`) and any PAC-50 write authorization. The 403
   description says which 403s come before the 400s and which after. The
   challenge's example (no org, `provider_type: saml`) is a 403 under the contract
   as under the code. This agrees with §0 rebase item 1 (insert into PAC-50's step
   2) and with `setSsoProviderState` step 1, which were already right and are
   unchanged.
2. **AC-6 can now be set up.** The list holds at most one item for any org
   (both domains unique, exact join), and `SsoProviderList.providers` says so and
   why. The array is kept (§2.7). Manifest AC-6 now tests zero and one item through
   the API, and covers the multi-item shape by validating a constructed
   `SsoProviderList` of two `SsoProvider` items against the bundle's schema. The
   spec's AC-6 carries a contract-stage note saying the same; its intent (the shape
   holds for any count) is unchanged, so it was not sent back to the spec.
3. **The `TestUpsertSSOProvider_Update` row is corrected** in §4 and in the
   manifest: it does not break, it asserts only `ProviderName` (`:84`) and writes
   `Enabled: true` twice. Both rows now require the plan to name new assertions: a
   replace keeps `enabled = false` and keeps `enabled = true`; a replace replaces
   every other configuration column, including one cleared to `""` (a SAML value
   and the secret), and sets `updated_at`. Spec §6 made the same claim about the
   test; it now carries a contract-stage correction.

**Non-blocking**

1. **`ssoOidcCallback` 404** now says: no active row, none or suspended (PAC-22);
   a sign-in started before a suspension that reaches the callback after it gets
   this 404 and no session; also returned when the lookup errors. Added to §1 and
   to the manifest's `operationsChanged` (description only).
2. **The `startSsoLogin` 500 wording** no longer reads as "reachable only for old
   rows". Only the 501 is; the 500 (issuer discovery failed) stays reachable for
   any row whose discovery fails, including a row written since PAC-22 with an
   unreachable or whitespace issuer. Fixed in the operation, §1 and the manifest.
3. **Atomicity of `setSsoProviderState`.** The state write and its
   `sso_provider_written` record are one transaction, as PAC-50's recorded writes
   already are; after a 500 the state is the one stored before the request and
   nothing is recorded; a consumed authorization stays consumed. Step 7 and the
   500 description say so (§2.5).
4. **`MIGRATION.md`.** The row stays `handwritten`, because the register describes
   the handler that exists and none does yet. Its notes column now says that the
   implementation uses the generated server interface and flips the row to
   `generated` in the same PR. Also in the manifest (`followUps.migrationRegister`).
5. **Web types (factory §2 gate 4).** Rather than wait for revision 2, revision 1.1
   ships with its paired web branch now: `smart-calendar-flow`
   `factory/pac-22-contract-types`, regenerated by `REGEN=1 web-gate` against this
   branch (gate results in the manifest). Revision 2 regenerates it again from the
   rebased bundle, and that is the run that counts (`followUps.webTypes`).
6. **Duplicate `state` keys** are refused: 400 `duplicate property: state`, in step
   2 in body order, so no authorization is spent. "Last one wins" was rejected
   (§2.5). Added to the 400 list, to `SsoProviderStateRequest` and to manifest
   AC-12.
7. **OWNER: OQ-5** (two writes, two step-ups). Unchanged; still open for the owner
   in the spec.
8. **OWNER: OQ-6** (OIDC public clients). Unchanged; still open for the owner.
9. **OWNER: OQ-7** (the kill switch may need the operator). Unchanged; still open
   for the owner.
10. **OWNER: subdomain rows stored under `main`'s suffix rule.** Recorded in the
    spec as OQ-8, with the factory's answer D-11, assumed pending owner
    confirmation: such a row is the operator's to remove, and the API is not
    widened to reach it. Giving the transition, the list or the delete a suffix
    rule again would let an organisation act on or see a domain it does not
    exactly own, which is the widening AC-21 and PAC-50 close. Nothing is deleted
    or rewritten.

## Revision 1.2 — 2026-10-28

This revision transcribes spec v3.4 (`4f3c609`, merged into this branch), which
changed the interface after revision 1.1 was challenged. It is not an answer to a
contract challenge; it needs one (contract-challenger, next). Re-checked against
`origin/main` `69ffa92`, merged into this branch with the spec. PAC-50 is still
unmerged (PR #212 open, `origin/impl/pac-50-sso-write-gate-backend` at `51aa70a`),
so this is not revision 2 and the rebase in §0 is still owed.

**Changes, each traced to the spec**

1. **Omitted client secret kept on a replace** (spec §3.2, D-12; AC-22, AC-23).
   `oidc_client_secret` leaves `SsoProviderCreateRequest.required`; supplied, it
   replaces; `""`/`null` refused with `oidc_client_secret must not be empty`
   (AC-26). §2.9.
2. **Kept only where it would be sent** (D-13; AC-24, AC-25). New check 6, after
   the domain match and before any PAC-50 authorization, with two messages. §2.9.
3. **Decided at the moment of storing** (D-14; AC-27, AC-28). Stated on
   `createSsoProvider`; `setSsoProviderState` step 6 says it writes only the
   state. §2.10.
4. **`setSsoProviderState`'s domain refusal is 403** (D-15, owner decision for
   PAC-50, 2026-10-27), moved from the 400 list to the 403 description. §2.5.
5. **Non-disclosure stated at the byte level** (spec §3.1, AC-1, AC-2 in v3.4):
   `listSsoProviders` and the `createSsoProvider` 201 say neither secret occurs in
   the body or headers, raw or JSON-escaped, in any case, and that nothing
   derived from either is in the shape. `SsoProvider` already had
   `additionalProperties: false` and no derived property.
6. **`has_oidc_client_secret`** says it can read true on a pre-PAC-22 `saml` row
   whose omitted-secret replace is still refused (spec §3.2, AC-25).
7. **`auth.notes.md`**: the "in plaintext to any authenticated org member" note
   is now history, and the U-21 and U-22 rows of its uncertainty table are marked
   resolved. No text in the contract says the secret is returned any more.

**Not changed**: the response shape (`SsoProvider`, `SsoProviderList`), the
transition's shape and order, the list's audience, `deleteSsoProvider`.

**Open questions** (spec §8): OQ-1 to OQ-4 are closed in the spec (D-1, D-4,
D-7) as factory assumptions pending owner confirmation; the owner-decisions record
(checked 2026-10-28) has no PAC-22 answer. OQ-5 to OQ-10 stay open for the owner,
each non-blocking with a recorded factory assumption; none changes this
interface unless the owner disagrees (OQ-10 would remove check 6 and make the
secret required again).
