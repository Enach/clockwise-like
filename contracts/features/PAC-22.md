# Contract — PAC-22: SSO configuration must not disclose identity-provider secrets

- **Spec**: `docs/specs/PAC-22.md`, v3.4 (the challenge of v3.3, 2026-10-28,
  accepted it with changes; v3.4 applies every one, see the spec's "Revision
  v3.4"). Revisions 1 and 1.1 of this contract were written against v3.2.
- **Linear**: https://linear.app/paceday/issue/PAC-22
- **Manifest**: `contracts/features/PAC-22.yaml`
- **Branch**: `factory/pac-22-contract`. It was cut from `origin/main` at `6d751f9`
  with the spec branch `factory/pac-22-spec` (`4a517e2`, spec v3.2) fast-forwarded
  in. Since then `origin/main` `69ffa92` has been merged in (`3ddbfbc`) and spec
  v3.4 (`factory/pac-22-spec` at `4f3c609`) merged in (`9ba22ba`), so the base is
  `69ffa92` plus spec `4f3c609`, as the manifest's `contractBase` says. The
  contract and the spec it transcribes travel together.
- **Status**: contract revision 1.3, draft, awaiting contract-challenger.
  Revision 1 (`79d63d9`) was challenged on 2026-10-27 (accept-with-changes, three
  blocking); revision 1.1 applied that challenge. Revision 1.2 (2026-10-28)
  transcribes spec v3.4: an omitted client secret is kept on a replace, bound to
  the issuer and client id, decided at the moment of storing (see "Revision 1.2"
  below). Revision 1.2 was challenged on 2026-10-28 (accept-with-changes, one
  blocking); revision 1.3 applies that challenge (see "Revision 1.3"). Revision 2
  is still the rebase onto PAC-50 (§0), and only its gates count for
  implementation.
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
   400 `unknown property`, its 403 `SsoWriteForbiddenResponse`) and its
   `provider_domain_mismatch` refusal with the status the owner decided: 403
   (spec D-15, owner 2026-10-27), as `setSsoProviderState` already says here.
   PAC-50's branch at `51aa70a` still answers 400; if PAC-50 merges with 400,
   revision 2 follows what merged for both operations and says so (D-15).
   Insert PAC-22's five validation checks into
   PAC-50's step 2 (body shape), in PAC-22's order, so that they still run before
   the authorization step. PAC-22's check 6 (an omitted client secret, against
   the stored configuration: revision 1.2) goes after PAC-50's domain check and
   before its authorization step; the store-time re-check (spec D-14) goes into
   PAC-50's recorded upsert, inside its transaction. Check 6 and the store-time
   re-check read **the row the write would be stored under** (revision 1.3).
   PAC-50 stores the domain lower-cased (its step 5), so after the rebase that is
   the row under the lower-cased domain; on `main` it is the row under the domain
   exactly as submitted, because the upsert conflicts on it unchanged
   (`sso_providers.go:63`) while the domain match trims and folds case
   (`domain.go:45-48`). Replace PAC-50's paragraph "`enabled` is always forced
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

- A body that is valid JSON but not an object (`[]`, `"active"`, `true`, `1`,
  `null`) is refused with `invalid JSON` (revision 1.3). The operation's body is
  defined as a JSON object, so a value of another type is not a body this
  operation can read, which is what `invalid JSON` says on every other operation
  whose decoder fails on it. **Rejected: `state must be 'active' or 'suspended'`**
  for these: it suggests the caller only got the value wrong, and for `"active"`
  it names the very value they sent.
- The lookup (step 5) and the write and record (steps 6, 7) see one and the same
  row (revision 1.3). If a `deleteSsoProvider` is stored after the lookup and
  before the state write, the answer is 404 `sso provider not found`, nothing is
  written and nothing is recorded, as for a row that was never there: D-9 says a
  request is never told it succeeded for a configuration that does not exist,
  and a record of a transition on a removed row would be exactly that. How the
  implementation holds the row (a lock, a conditional update that reports zero
  rows) is the plan's. **Rejected: answer 200 from the lookup's copy**: the
  caller would be told SSO is suspended (or active) for a configuration that no
  longer exists.

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
- **Check 6 reads the row the write would be stored under** (revision 1.3), not
  "the configuration for the domain" in some normalised sense. On `main` the
  upsert conflicts on the domain as submitted while the domain match trims and
  folds case, so a check 6 that lower-cased would let `{"domain":"ACME.com", …}`
  with no secret pass against a stored `acme.com` and then insert a new
  `ACME.com` row. Reading the conflict key makes that request a create, which
  has no secret to keep, so it is refused. After PAC-50 the key is the
  lower-cased domain, and the two readings coincide.

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
revision. The bundle holds 8 markers on `origin/main` `69ffa92` and 6 on this
branch (revision 1.3, assembler summary; revision 1 counted 12 on `6d751f9` and
10 on its branch).

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

## Challenge — 2026-10-28 (revision 1.2)

Applies to contract revision 1.2, commit `f42411f` on `factory/pac-22-contract`
(contract content `b3386cb`). Read against the committed artefacts only: this
document, the manifest, `contracts/openapi/paths/auth.yaml`, spec v3.4
(`docs/specs/PAC-22.md` on this branch), the handler and storage on this branch
(`backend/api/handlers_sso.go`, `backend/storage/sso_providers.go`,
`backend/domain/domain.go`, `backend/auth/sso_detect.go`), PAC-50's contract on
`origin/impl/pac-50-sso-write-gate-backend` at `51aa70a`, and the factory's
owner-decisions record (no PAC-22 entry). The challenger did not see the
author's reasoning beyond these files.

**Verdict**: accept-with-changes

**Checks run by the challenger** (nothing else is claimed):

- In the worktree on `f42411f` (`make` is not installed on this host, so the
  three steps of `make openapi-check` were run directly):
  `python3 scripts/openapi_assemble.py --check` → `OK: bundle is in sync (100
  paths)`, rc=0; `python3 scripts/openapi_migration_report.py --check` → `OK:
  MIGRATION.md covers all 121 operations (4 generated)`, rc=0; `bash
  scripts/openapi_gen_go.sh --check` → `OK: backend/api/gen matches the
  contract`, rc=0.
- Web half: `bash /c/Users/jouzu/paceday/.claude/factory/web-gate.sh
  factory/pac-22-contract-types factory/pac-22-contract` (no REGEN) on web
  `5bfdefd`: lint, typecheck, design-check, openapi-check, test (315 passed),
  coverage all PASS; `make verify rc=0`. Factory §2 gate 4 holds for
  revision 1.2.
- `sha256sum` of `contracts/openapi/openapi.yaml` at `b3386cb` and at `HEAD`:
  both `0deb2efd…9c038c3`. Matches `contractHash`; `b3386cb..HEAD` touches only
  `PAC-22.md` and `PAC-22.yaml`.
- `x-uncertain:` keys: 8 in `origin/main`'s bundle, 6 here (matches the
  manifest). None on any of the seven touched operations or on `SsoProvider`,
  `SsoProviderList`, `SsoProviderCreateRequest`, `SsoProviderStateRequest`,
  `SsoDetectResponse` (parsed with PyYAML).
- Not run: api-gate (the host checkout is on `main` and this branch is held by
  the `wt-pac22-contract` worktree, so the host cannot be switched to it),
  `go build`, the backend and mcp tests, e2e.

### Blocking

1. **The byte-level non-disclosure guarantee is stated for every secret, and no
   server can honour it.** `listSsoProviders` says "neither stored value occurs
   anywhere in the body or the headers, raw or JSON-escaped, in any letter
   case"; the `createSsoProvider` 201 says "The supplied secret … does not occur
   in the body or the headers"; the manifest repeats it for `listSsoProviders`.
   The schema puts no floor on the secret beyond `minLength: 1`, and the
   response legitimately carries caller-chosen strings and fixed tokens.
   - Request: `POST /api/admin/sso` with
     `{"domain":"acme.com","provider_name":"Okta","provider_type":"oidc","oidc_issuer":"https://acme.okta.com","oidc_client_id":"0oa1","oidc_client_secret":"okta"}`.
     The correct 201 contains `Okta` (`provider_name`) and `okta` (issuer), so a
     case-insensitive search finds the "secret". `"oidc_client_secret":"true"`
     is found in `"has_oidc_client_secret":true`; `"oidc"` in `provider_type`;
     `"a"` anywhere.
   - Spec AC-1 and AC-2 avoid this by choosing values that are "long, unique to
     the test". The contract dropped that precondition and turned a test design
     into a promise about every secret, which a contract test or a
     property-based test generated from the bundle will falsify.
   - **Fix** (description text only, three places: the `listSsoProviders`
     description, the `createSsoProvider` 201, manifest `operationsChanged[0]`):
     say that no property of the response carries either secret or anything
     derived from it, and that the byte search (raw, JSON-escaped, any case,
     body and headers) holds for secrets that do not occur in the response's
     non-secret values, as AC-1 arranges. Do not add a `minLength` floor to the
     secret: the spec does not ask for that behaviour.

### Requests wrongly permitted

None beyond the non-blocking items below. Attacked, each against the text of
revision 1.2:

- `POST /api/admin/sso`, stored `acme.com` I1/C/S1, body with I1, C and no
  secret → check 6 passes, 201, secret kept (AC-22). Same with `oidc_issuer`
  `"I1/"` → 400 `… when oidc_issuer or oidc_client_id changes` (byte
  comparison, AC-24).
- Same with `"oidc_client_secret": ""` and `null` → check 5, `must not be
  empty`, before check 6 and before any authorization (AC-26, AC-17).
- Stored pre-PAC-22 `saml` row with a non-empty client secret, OIDC body with
  no secret → check 6 `oidc_client_secret is required` (row not `oidc`),
  although `has_oidc_client_secret` reads true (AC-25). Stated on the schema.
- No row, secret absent → check 6 `oidc_client_secret is required`, after the
  domain match, so a caller targeting another org's domain gets the 403 first
  and never learns whether a row exists there.
- `{"provider_type":"saml","saml_cert":null, …}` → `saml_cert is not
  supported` (check 2 before check 4).
- `PUT /api/admin/sso/acme.com/state` with
  `{"state":"suspended","oidc_client_secret":"x"}` → `unknown property:
  oidc_client_secret`; `{"state":"bogus","x":1}` → `unknown property: x`
  (property checks before the value check);
  `{"state":"active","state":"suspended"}` → `duplicate property: state`;
  `{}` and `{"state":null}` → `state must be …`.
- `PUT /api/admin/sso/sub.acme.com/state`, `…/other.com/state`,
  `…/stepup/state` → 403 `provider_domain_mismatch` at step 3, authorization
  unspent (AC-21, D-15).

### Responses the client cannot handle

None. There is no hand-written consumer: on `origin/main` of
smart-calendar-flow, `git grep "admin/sso\|SsoProvider\|has_oidc_client_secret"
-- src ':!src/api/generated'` finds nothing. Every `SsoProvider` property is
required and every column it projects is `NOT NULL`
(`008_sso_providers.up.sql`), so no property can be absent or null.
`created_at` and `updated_at` are Go `time.Time`, RFC 3339, matching
`format: date-time`.

### Consumers affected

- smart-calendar-flow `src/api/generated/*`: regenerated on
  `factory/pac-22-contract-types` `5bfdefd`, in sync with this bundle (web-gate
  above).
- clockwise-like `backend/api/gen`: in sync (`openapi_gen_go.sh --check`).
- `mcp/`, `e2e/`: `grep -rn "admin/sso\|SsoProvider\|sso_provider"` finds
  nothing.
- `backend/**/*_test.go`: no test on this branch calls `/api/admin/sso` or
  decodes the PascalCase keys (grep). PAC-50's tests on its branch are already
  listed in §4.

### Contract/implementation divergences

None new. `SsoProviderCreateRequest` is `additionalProperties: false` while the
handler ignores undeclared properties (`handlers_sso.go:174-185`). The
description states this, PAC-50 closes it on rebase, and the previous challenge
accepted it.

### Uncertainties not actually resolved

None. U-21 and U-22 stay resolved, as the 2026-10-27 challenge accepted. This
revision adds no marker and removes none.

### Verified clean

- **Acceptance tests.** The manifest transcribes every spec v3.4 criterion
  (AC-1 to AC-12, AC-14 to AC-28; AC-13 withdrawn). AC-22 to AC-28 match the
  spec's text, including AC-27 not asserting the authorization on a store-time
  refusal (D-14) and AC-17 covering AC-24, AC-25 and AC-26. No silent
  narrowing.
- **Check order vs. AC-17.** Checks 1-6 all precede PAC-50's authorization.
  Check 6 follows the domain match, so it only ever reads the caller's own
  organisation's row. Rebase item 1 places it between PAC-50's steps 3 and 4,
  the same position (PAC-50's step 3 is the domain, step 4 the authorization,
  read at `51aa70a`).
- **The redirect D-13 closes.** No body can change `oidc_issuer` or
  `oidc_client_id` while keeping the stored secret: check 6 refuses it at
  check time and the store-time re-check refuses it in the race (AC-27). No
  body can create a row with an empty secret: a create with the secret absent
  is refused at check 6 and at the store.
- **The transition carries no configuration value** (AC-12).
  `SsoProviderStateRequest` has one property and `additionalProperties:
  false`, and the server-side `unknown property` refusal is stated
  independently of PAC-50's decoder change.
- **Suspension semantics** are what the code does. `GetSSOProviderByDomain`
  filters `enabled = true` (`sso_providers.go:51`), and detection
  (`sso_detect.go:37`), start (`handlers_sso.go:51`) and callback (`:112`) all
  use it. Step 5's "whatever its state" correctly requires a different lookup.
- **Store-time decisions need no migration.** There is no trigger on
  `sso_providers`, only the upsert sets `updated_at` (`sso_providers.go:73`),
  and `enabled` is a plain column.
- **Security conventions.** The new operation inherits `bearerAuth` (no
  `security` key); the three public routes keep `security: []`. Both new
  response schemas are `additionalProperties: false` and carry no
  secret-derived property.
- **Rejected alternatives in §2.9-§2.10** (sentinel value, rotate-secret
  operation, keep-regardless, empty-clears, check-time-only, If-Match) are each
  rejected against a spec decision (D-12, D-13, D-14), not on taste.

### Non-blocking

1. **Check 6 does not name the key it looks up.** "Against the stored
   configuration for the domain" leaves open whether the submitted `domain` is
   used as given or lower-cased. On `main` the upsert conflicts on the domain
   as submitted (`sso_providers.go:63`), while `DomainMatchesOrg` trims and
   folds case (`domain.go:45-48`). So `{"domain":"ACME.com", …}` with no secret
   and a stored `acme.com` row could pass a lower-cased check 6 and then insert
   a new `ACME.com` row. The store-time rule forbids an empty secret there but
   does not say which row "the configuration" is. PAC-50 stores the domain
   lower-cased (its step 5), so after the rebase the safe reading is "the row
   the write would be stored under". Say that on check 6, and add it to §0
   rebase item 1.
2. **setSsoProviderState vs. a concurrent delete.** Steps 5-7 are not stated to
   be atomic against `deleteSsoProvider`. A delete that lands between step 5
   and step 6 lets an implementation answer 200, and record a transition, for
   a row that no longer exists, which D-9 ("never success for a configuration
   that does not exist") argues against. State that the lookup and the write
   see the same row (or that a write that finds no row is the 404).
3. **Non-object bodies on setSsoProviderState** (`[]`, `"active"`, `true`) are
   valid JSON but are not assigned a message. Say which of `invalid JSON` or
   `state must be …` they get.
4. **Step 3's "as createSsoProvider answers on `main` today"** is true only for
   a foreign domain: on `main`, createSsoProvider accepts a subdomain (suffix
   rule). Reword to "as createSsoProvider answers a foreign domain on `main`".
5. **§0 rebase item 1 says to keep PAC-50's "400 … `provider_domain_mismatch`"**
   for createSsoProvider. The owner decided 403 (D-15), and setSsoProviderState
   here already says 403. The rebase follows what merged, so this is only
   wording, but the two sentences should agree.
6. **Stale facts in this document.** The header still says the branch was cut
   at `6d751f9` with spec `4a517e2` (the manifest says `69ffa92` and `4f3c609`).
   §5 says the marker count went "from 12 to 10" (now 8 on `main`, 6 here).
7. **Mixed-case rows stored on `main`** (createSsoProvider stores the domain as
   given) are, like D-11's subdomain rows, not listed (exact join) and not
   reachable by setSsoProviderState (step 5 lower-cases), yet
   `GET /api/auth/sso/Acme.com` still uses them. D-11's release note should
   cover any domain that is not exactly, byte for byte, an organisation's
   domain, not only subdomains.
8. **OWNER:** OQ-10 / D-12: "omitted means kept, never cleared" for the SSO
   client secret extends the owner's PAC-24 decision for LLM keys. No owner
   answer exists for PAC-22.
9. **OWNER:** OQ-9 / D-13: a changed issuer or client id requires the secret
   again.
10. **OWNER:** OQ-5 (two writes to fix and resume), OQ-6 (no OIDC public
    clients), OQ-7 (the kill switch may need the operator) and OQ-8 / D-11
    (subdomain rows are operator-only) are still open, unchanged by revision
    1.2.

## Revision 1.3 — 2026-10-28

This revision answers the challenge of revision 1.2 above (applies to `f42411f`,
contract content `b3386cb`). The challenge accepted revision 1.2 with changes.
Every change it lists is applied, in the challenge's order. The contract content
and the regenerated Go code are commit `539423c`; bundle sha256
`cf385679…f392`. Every change is description text: no schema, property,
`required` list, status code or operation changed, so the generated Go and web
code change only in doc comments. PAC-50 is still unmerged, so this is not
revision 2; the rebase in §0 is still owed.

**Blocking**

1. **The byte-level guarantee is scoped to what a server can honour.** In the
   three places the challenge names (`listSsoProviders` description,
   `createSsoProvider` 201, manifest `operationsChanged[0]`), the guarantee is
   now: no property of the response carries either secret or anything derived
   from it, under any name; and the byte search (raw, JSON-escaped, any letter
   case, body and headers) holds for secrets that do not occur in the response's
   non-secret values, as spec AC-1 and AC-2 arrange with long, test-unique values.
   Each place names the counter-example (a secret of `okta` or `true` coinciding
   with `provider_name`, `oidc_issuer` or `has_oidc_client_secret: true`) so the
   precondition is not dropped again. No `minLength` floor was added to the
   secret: the spec does not ask for it. The manifest's `createSsoProvider` entry
   says the same. The spec's §3.1 already states the guarantee as a property of
   the described shape plus the byte search of AC-1, so the spec is unchanged.

**Non-blocking**

1. **Check 6 names its key.** Check 6 now reads "the row this write would be
   stored under, that is the row the upsert would conflict with": on `main`, the
   row whose `domain` is byte-for-byte the submitted `domain`
   (`sso_providers.go:63` conflicts on it unchanged, while `DomainMatchesOrg`
   trims and folds case, `domain.go:45-48`); after PAC-50, the row under the
   lower-cased domain PAC-50 stores. `{"domain":"ACME.com", …}` with the secret
   absent is therefore a create when no `ACME.com` row exists, and refused
   `oidc_client_secret is required`; it can no longer pass against `acme.com` and
   insert a new row. The store-time re-check uses the same key. §0 rebase item 1
   and §2.9 say so; the manifest's `createSsoProvider` entry too.
2. **`setSsoProviderState` vs. a concurrent delete.** Steps 5 to 7 see one and
   the same row: the state write and its record apply to the row the lookup
   found and nothing else. A configuration removed after the lookup and before
   the state write is stored gets 404 `sso provider not found`, nothing written,
   nothing recorded, the authorization spent (D-9). Stated after step 7 and on
   the 404; rationale and the rejected alternative in §2.5; manifest
   `operationsAdded`. How the row is held is the plan's.
3. **Non-object bodies** (`[]`, `"active"`, `true`, `1`, `null`) get `invalid
   JSON`. Stated in step 2, on the 400, and on `SsoProviderStateRequest`;
   `state must be …` rejected for them in §2.5.
4. **Step 3** now says the 403 is "as createSsoProvider answers a foreign domain
   on `main` today", and that `main`'s createSsoProvider still accepts a
   subdomain under its suffix rule while this operation never does.
5. **§0 rebase item 1** no longer says to keep PAC-50's "400 …
   `provider_domain_mismatch`". It keeps PAC-50's refusal with the owner's status,
   403 (D-15), as `setSsoProviderState` says, and follows what merged for both
   operations if PAC-50 merges with 400.
6. **Stale facts.** The header now gives the original cut (`6d751f9`, spec
   `4a517e2`) and the current base (`origin/main` `69ffa92` merged in `3ddbfbc`,
   spec `4f3c609` merged in `9ba22ba`), as the manifest's `contractBase` does. §5
   now says 8 `x-uncertain` markers on `origin/main` `69ffa92` and 6 on this
   branch (re-counted for this revision: `grep -c "x-uncertain:"` on both
   bundles, and the assembler's summary `x-uncertain: 6`).
7. **Mixed-case rows.** Spec D-11 carries a contract-stage note (not a behaviour
   change): "not exactly an organisation's domain" means byte for byte, and
   covers rows stored on `main` under another casing or with surrounding
   whitespace as well as subdomain rows. Such a row is not listed, cannot be
   reached by `setSsoProviderState`, is not found by detection (it lower-cases
   the email's domain, `sso_detect.go:37`), yet `GET /api/auth/sso/Acme.com` and
   its callback still use it (`handlers_sso.go:51`, `:112`, the path segment as
   given). The release note names all of them; D-11's answer (operator only, API
   not widened) applies to all of them. Manifest `decisionsAddedToSpec` records
   the note.
8. **OWNER: OQ-10 / D-12** ("omitted means kept, never cleared" for the SSO
   client secret). The owner-decisions record, read again on 2026-10-28, has no
   PAC-22 entry; its LLM decision ("Keys stay write-only (PAC-24)") is about
   language-model keys. D-12 stays the factory's assumption, pending owner
   confirmation, as the spec records it. It is non-blocking: it never deletes a
   stored secret and widens nobody's access. If the owner answers otherwise,
   check 6 goes and the secret becomes required again (spec OQ-10).
9. **OWNER: OQ-9 / D-13** (a changed issuer or client id requires the secret).
   No owner answer; stays the factory's assumption, pending owner confirmation.
   Non-blocking: the alternative is the one that can redirect the secret.
10. **OWNER: OQ-5, OQ-6, OQ-7, OQ-8 / D-11.** No owner answer for any of them;
    each stays open with its recorded factory assumption, unchanged by this
    revision (D-11 only gains the note in item 7).

**Not changed**: the response shape, the transition's shape and order of checks,
the list's audience, `deleteSsoProvider`, every 400/403/404 message text.

## Challenge — 2026-10-28 (revision 1.3)

Applies to contract revision 1.3, commit `a88b495` on `factory/pac-22-contract`
(contract content `539423c`), against spec v3.4 as accepted on
`factory/pac-22-spec` `bf5a818`. Run by the factory session that also holds
PAC-22's stage 4: it is not the contract author and read only the committed
artefacts, but its independence is narrower than a separate challenger
session's, which the stage 6 reviewer should weigh.

**Verdict**: accept

**Checks run by the challenger** (nothing else is claimed), in the worktree on
`a88b495`: `python scripts/openapi_assemble.py --check` → `OK: bundle is in
sync (100 paths)`; `python scripts/openapi_migration_report.py --check` → `OK:
MIGRATION.md covers all 121 operations (4 generated)`; `sha256sum
contracts/openapi/openapi.yaml` → `cf385679…f392`, equal to the manifest's
`contractHash`. The api-gate and web-gate are run again by stage 4 on the
implementation branches, and their output is in those PRs.

### Requests wrongly permitted

None. Attacked against the text of revision 1.3:

- `POST /api/admin/sso` `{"domain":"ACME.com", …, no oidc_client_secret}` with
  only an `acme.com` row stored → check 6 now reads the row the upsert would
  conflict with (`ACME.com`), finds none, 400 `oidc_client_secret is required`.
  The revision 1.2 gap (pass against `acme.com`, insert `ACME.com` with an
  empty secret) is closed.
- Same body, `domain` `" acme.com"` → the same: the key is the submitted value,
  not the trimmed one, and no row is created with an empty secret.
- `{"provider_type":"oidc", …, "saml_issuer":null}` → check 2, `saml_issuer is
  not supported`, whatever the value.
- `{"oidc_client_secret":null, …}` and `""` → check 5, `oidc_client_secret must
  not be empty`, before check 6.
- Stored I1/C/S1, body I1, `C ` (trailing space), no secret → check 6, the
  "changes" message (byte comparison, no normalisation).
- `PUT /api/admin/sso/acme.com/state` with `[]`, `"active"`, `true`, `null` →
  `invalid JSON` (stated in step 2 since 1.3).

### Responses the client cannot handle

None. No hand-written consumer of these operations exists in
smart-calendar-flow `origin/main` `aa5c52e` outside `src/api/generated/`
(`git grep "admin/sso\|SsoProvider\|oidc_client"`, empty). Every `SsoProvider`
property is required, non-nullable and projected from a `NOT NULL` column.

### Consumers affected

- smart-calendar-flow `src/api/generated/*` only (regenerated by the paired web
  PR). `mcp/` and `e2e/`: no SSO reference (`git grep`, empty).

### Contract/implementation divergences

1. **Non-blocking, sequencing.** `setSsoProviderState` says in bold that it is
   not implementable before PAC-50 merges. If PAC-22's projection and
   validation half is implemented on `main` before PAC-50 (as spec challenge
   v3.4 non-blocking 1 argues), the merged bundle describes an operation the
   server does not route: `PUT /api/admin/sso/{domain}/state` answers chi's
   plain 404 until revision 2. The description already says so, so a reader of
   the contract is not misled about the gap; the stage-4 PR must state it, and
   revision 2 (the rebase onto PAC-50) closes it. No schema change resolves it
   short of removing the operation, which would lose the reviewed text; that is
   the contract author's call for revision 2.
2. `SsoProviderCreateRequest` is `additionalProperties: false` while the
   handler ignores undeclared properties other than the three SAML ones.
   Stated in the description, unchanged since revision 1, closed by PAC-50 on
   rebase.

### Uncertainties not actually resolved

None. U-21 and U-22 are resolved by the schema and are pinned by the stage-4
tests (create validation and the projection).

### Verified clean

- Revision 1.3 applies the 1.2 challenge: the scoped byte-level guarantee in
  the three places named, the check-6 key, the transition's same-row rule
  against a concurrent delete, non-object bodies, step 3's wording, the §0
  rebase item, the stale header facts, and the D-11 note. Each read in
  `contracts/openapi/paths/auth.yaml` and this document.
- `acceptanceTests` still transcribes spec v3.4's AC-1 to AC-12 and AC-14 to
  AC-28 (AC-13 withdrawn); no narrowing.
- No secret-derived property in `SsoProvider` or `SsoProviderList` (both
  `additionalProperties: false`; the only secret-related properties are the
  two booleans).
