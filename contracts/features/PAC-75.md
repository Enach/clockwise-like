# Contract: PAC-75, PAC-50 follow-ups (ASCII-only domain match, step-up denial bound, denial text, options failure, PAC-50 wording) + PAC-95 (`unavailable`)

- **Linear**: PAC-75, https://linear.app/paceday/issue/PAC-75 (folds in PAC-95,
  https://linear.app/paceday/issue/PAC-95; close PAC-95 as "done in PAC-75" when
  the api PR merges, not before).
- **Spec**: `docs/specs/PAC-75.md` on `factory/pac-75-spec` (`4fbe015`), accepted
  2026-11-14.
- **Manifest**: `contracts/features/PAC-75.yaml`.
- **Fragments**: `contracts/openapi/paths/auth.yaml` (createSsoProvider,
  deleteSsoProvider, setSsoProviderState, ssoAdminStepupCallback,
  `SsoProviderCreateRequest.domain`, `SsoWriteDomainMismatch`) and the API-level
  `info.description` held in `scripts/openapi_assemble.py` (`INFO`, budget
  exception (3)).
- **Base**: `origin/main` `fd4e7b1` (bundle `85187c94…`), merged into the spec
  branch with no conflict (the spec branch was already at `fd4e7b1`).
- **Revision**: 1.1 (revision 1 by contract-author; 1.1 after the challenge
  below, same day).
- **Open questions applied** (factory defaults, owner may still answer):
  OQ-1 = 5 individually recorded denials per user per 10 minutes, the rest
  counted; OQ-2 = no (a budget expiry *during* checks 0–4 keeps PAC-81's rule).
  OQ-3 resolved here: the reason is **`unavailable`**; the left-out count is the
  column **`left_out`** of the new table **`sso_stepup_denial_windows`**, one row
  per (user, 10-minute window).

## 0. The rules this contract fixes

1. **Domain match.** An SSO write or delete names the organisation's domain
   when the two values are equal byte for byte except that ASCII `A`–`Z` may
   stand for `a`–`z`. Nothing else folds. What is accepted is stored and
   targeted as the organisation domain's own bytes.
2. **Denial bound.** Granted step-up outcomes are always recorded. Denied ones
   are recorded individually up to 5 per user per UTC-aligned 10-minute window;
   each one beyond is counted (`left_out`) in the same transaction, in the
   database, so the bound holds under concurrency and across api processes.
   Nothing a client sees changes.
3. **Budget at the record step (PAC-95).** Once the administrator check has
   passed, or the outcome is decided, a budget expiry never says `not_admin`
   (or any earlier check's label): a granted-but-unfinished step-up answers the
   new `unavailable`, a decided denial answers its own reason.
4. **Web display.** A reason or code the page does not know (including any
   `Object.prototype` name) is shown as a plain denial / omitted. A failed
   options request shows an error line instead of hiding the section.

## 1. What changes at the HTTP boundary

No operation is added or removed, no request or response schema changes shape.

| Change | Where |
|---|---|
| `Location` pattern gains `unavailable` (ten reasons); description says it is budget-only, never recorded, and why the redirect enum = recorded enum + `unavailable` | ssoAdminStepupCallback 302 |
| "Every outcome … is recorded" replaced by: grants always recorded; denials recorded up to 5 per user per 10-minute UTC-aligned window, the rest counted in `sso_stepup_denial_windows.left_out`; client-visible behaviour unchanged | ssoAdminStepupCallback description |
| "Time budget" paragraph: PAC-81's rule during checks 0–4 unchanged; from check 4b and after the decision, `unavailable` (granted) or the decided reason (denied), with the decided redirect's `purpose` | ssoAdminStepupCallback description; `info.description` exception (3) |
| 500 also covers a failed or >10 s-waiting window count | ssoAdminStepupCallback 500 |
| DOMAIN MATCH rule (ASCII-only case; no trim, no IDNA, no normalisation; examples for `ſ`, Kelvin, `İ`/`ı`, fullwidth, U-label vs A-label) | createSsoProvider step 3 |
| Stored / looked-up / deleted value is the org domain's exact bytes (was "the lower-cased domain") | createSsoProvider steps 4, 6, 201; deleteSsoProvider; setSsoProviderState steps 3, 5; `SsoProviderCreateRequest.domain` |
| `{domain}` is "as the router yields it, not trimmed" (was "lower-cased and not trimmed"): the Unicode lower-casing before the comparison is gone; canonical escapes are decoded, any other escape stays literal and never matches (challenge C-2) | deleteSsoProvider, setSsoProviderState |
| `SsoWriteDomainMismatch` lists the non-ASCII-fold case | schema description |

Status code of every refusal is unchanged (403 `provider_domain_mismatch`,
owner 2026-10-27). setSsoProviderState is still not served (API-066, PAC-50);
its text is aligned so it does not describe a rule no other SSO write follows.

## 2. Domain match (binding)

### 2.1 The predicate

`match(sent, org)` is true iff `len(sent) == len(org)` (bytes) and for every
byte index `i`: `asciiLower(sent[i]) == asciiLower(org[i])`, where `asciiLower`
maps `0x41..0x5A` to `0x61..0x7A` and leaves every other byte unchanged. An
empty `sent` never matches. No trimming, no IDNA/punycode conversion, no NFC/NFKC,
no Unicode case mapping, no percent-decoding beyond the router's decoding of the
path segment. (Go: replacing `strings.EqualFold` in `domain.DomainMatchesOrg`
with an ASCII-only comparison; the function has exactly two callers,
`handlers_sso.go:239` and `:466`.)

Consequences the tests pin (spec AC-1..AC-6):

| org domain | sent | result | why |
|---|---|---|---|
| `acme.com` | `ACME.com`, `AcMe.CoM` | match | ASCII case |
| `acmes.com` | `acmeſ.com` (U+017F) | 403 | `ſ` is 2 bytes; not ASCII |
| `kcme.com` | `Kcme.com` (U+212A Kelvin) | 403 | 3 bytes; not ASCII |
| `istanbul.com` | `İstanbul.com` (U+0130), `ıstanbul.com` (U+0131) | 403 | non-ASCII |
| `istanbul.com` | `ISTANBUL.COM` | match | ASCII |
| `ıstanbul.com` (non-ASCII org) | `ıstanbul.com` | match | same bytes |
| `ıstanbul.com` | `Istanbul.com`, `istanbul.com` | 403 | `ı` ≠ `i` |
| `straße.de` | `STRAßE.DE` | match | ASCII letters fold, `ß` same bytes |
| `straße.de` | `STRASSE.DE`, `straẞe.de` (U+1E9E) | 403 | no full or non-ASCII folding |
| `acme.com` | `ａｃｍｅ.com` (fullwidth) | 403 | non-ASCII; no NFKC |
| `xn--bcher-kva.example` | `XN--BCHER-KVA.EXAMPLE` | match | A-label is ASCII |
| `xn--bcher-kva.example` | `bücher.example` | 403 | no IDNA |
| `acme.com` | `acme.com.`, ` acme.com`, `sub.acme.com` | 403 | unchanged (PAC-50) |

### 2.2 What is stored and targeted

After the match, the handler replaces the request's domain with `org.Domain`
(its bytes) for every later step: the kept-secret read (step 4), the store
(step 6) and the response; the delete's target; setSsoProviderState's lookup.
So AC-3/AC-4 hold by construction ("stored domain equals the org domain byte
for byte") and `listSsoProviders`' byte-equality join (`sso_providers.go:316-326`)
always shows what was written. The storage predicates (`lower(domain) = $1`,
the `lower(domain)` unique index) are unchanged; for an org domain, which is
Go-lower-cased at creation (`domain.ExtractDomain`), PostgreSQL's `lower()` is
the identity on it: always for ASCII, and observed for the non-ASCII rows of
§2.1 under PG 16.15 `en_US.utf8` (challenge V-4).

### 2.3 Existing lookalike rows

Not deleted, and after this change not deletable through the API (spec D-2).
Release note carries this read-only operator query:

```sql
SELECT s.id, s.domain, s.created_at
FROM sso_providers s
WHERE NOT EXISTS (SELECT 1 FROM organizations o WHERE o.domain = s.domain);
```

and says such a row is removed by hand (`DELETE FROM sso_providers WHERE id = …`)
after checking it is not an organisation's. Those rows are inert for sign-in
(sign-in emails are ASCII).

## 3. Step-up denial bound (non-HTTP interface, binding)

### 3.1 Constants

`StepupDenialRecordBound = 5`, `StepupDenialWindow = 10 * time.Minute`, Go
constants in `storage` (not environment variables: the owner treated them as a
product default, OQ-1). The window holding instant `t` starts at
`floor(unix_seconds(t) / 600) * 600` (UTC), where `t` is the outcome's `Now`
(`SSOStepupOutcome.Now`, the handler's injected clock, read when the outcome is
decided). Not the database clock: tests inject time (spec AC-11), and every api
process computes the same window for the same instant.

### 3.2 Migration 039 (verbatim DDL)

`backend/storage/migrations/039_sso_stepup_denial_windows.up.sql`:

```sql
-- 039_sso_stepup_denial_windows.up.sql — PAC-75 (contract §3): denied admin
-- step-up outcomes are recorded individually up to a bound per user and
-- 10-minute window; the rest are counted here. The row is also the lock that
-- makes the bound hold under concurrency and across api processes.
CREATE TABLE sso_stepup_denial_windows (
    user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    window_start     TIMESTAMPTZ NOT NULL,
    recorded         INTEGER     NOT NULL CHECK (recorded >= 1),
    left_out         INTEGER     NOT NULL DEFAULT 0 CHECK (left_out >= 0),
    last_left_out_at TIMESTAMPTZ NULL,
    PRIMARY KEY (user_id, window_start),
    CHECK (extract(epoch FROM window_start) % 600 = 0),
    CHECK ((left_out = 0) = (last_left_out_at IS NULL))
);
```

`039_sso_stepup_denial_windows.down.sql`:

```sql
DROP TABLE IF EXISTS sso_stepup_denial_windows;
```

The down step loses only the left-out counts; no `sso_events` row is touched
(spec §7). Number 039: main's latest is 038 (PAC-82) and no `origin/*` branch
carries 039+ on 2026-11-14; renumber (manifest and md only) if that changes.

### 3.3 The record transaction

`storage.RecordSSOStepupOutcome`, one transaction, `READ COMMITTED`, statements
in this order:

1. The PAC-71 re-check (`SELECT session_generation FROM users WHERE id = $1 FOR
   KEY SHARE`), unchanged. When the context carries no session guard, or the
   guard's user is not `o.UserID`, the transaction instead (or additionally)
   runs `SELECT 1 FROM users WHERE id = $1 FOR KEY SHARE` for `o.UserID`. Either
   way the acting user's row is KEY SHARE-locked before step 3 (lock order,
   §3.5).
2. Granted outcome: today's statements (tenant pin, grant, `sso_stepup` event),
   no window statement. Stop here.
3. Denied outcome: `SELECT set_config('lock_timeout', '<storage.LinkLockTimeout ms>ms', true)`
   (PAC-101's 10 s, transaction-scoped), then exactly one statement:

   ```sql
   INSERT INTO sso_stepup_denial_windows AS w (user_id, window_start, recorded)
   VALUES ($1, $2, 1)
   ON CONFLICT (user_id, window_start) DO UPDATE SET
     recorded         = w.recorded + CASE WHEN w.recorded < $3 THEN 1 ELSE 0 END,
     left_out         = w.left_out + CASE WHEN w.recorded < $3 THEN 0 ELSE 1 END,
     last_left_out_at = CASE WHEN w.recorded < $3 THEN w.last_left_out_at ELSE $4 END
   RETURNING left_out;
   ```

   `$1` user, `$2` window start, `$3` `StepupDenialRecordBound`, `$4` `o.Now`.
   **This outcome is recorded individually iff the returned `left_out` is 0.**
   (`left_out` grows only once `recorded` has reached the bound, and `recorded`
   never grows after that, so `left_out = 0` after this statement means this
   statement incremented `recorded`.)
4. If recorded individually: today's `sso_stepup` event insert, details
   unchanged (`{org_id, provider, outcome:"denied", reason[, purpose]}`).
   Otherwise no event row.
5. `COMMIT`. A failure anywhere rolls everything back (no count without its
   event, no event without its count); the handler answers today's 500. A wait
   at step 3 longer than the lock timeout is SQLSTATE 55P03, the same 500.

After the commit, when the returned `left_out` is exactly 1 (the first left-out
outcome of that user's window), the api logs one line:
`sso step-up: denied outcomes of user <uuid> in window <RFC3339 start> beyond 5 are counted, not recorded (sso_stepup_denial_windows.left_out)`.
At most one such line per user per window, so the log is not the flood either.

A granted outcome whose tenant pin fails (`ErrTenantPinned`, re-recorded as
`tid_changed`) is a denied outcome and goes through step 3.

### 3.4 Why it is race-free and replica-safe

The decision "recorded or counted" is made by one `INSERT … ON CONFLICT DO
UPDATE` on the row keyed `(user_id, window_start)`. PostgreSQL guarantees that
statement either inserts or updates atomically even under concurrency (READ
COMMITTED): a concurrent second inserter waits on the first's uncommitted
index entry, then takes the update path on the committed row version; two
updaters serialise on the row lock and the second's `SET` reads the first's
committed values. The decision lives in the database, not in any process, so
any number of api replicas share it. Window boundaries computed by two
processes with a few seconds of clock skew can put two near-boundary outcomes
in different windows; the bound per window still holds (each window row is its
own counter), so the worst case is 10 individually recorded denials in any
sliding 10 minutes, never more.

### 3.5 Lock order (PAC-71, PAC-101, PAC-82, PAC-84)

A denied record takes, in order: `users` row of the actor (KEY SHARE, step 1)
→ one `sso_stepup_denial_windows` row (exclusive, step 3; its FK check needs
the KEY SHARE already held) → on the event insert, KEY SHARE on the
`organizations` row (FK) when `org_id` is set. It takes no advisory lock and
touches no `scheduling_links`, `bookings`, `sso_stepup_grants` or
`encryption_keys` row.

- PAC-82 (advisory classes 8201–8203) and PAC-84 (8400): disjoint, no common
  object.
- PAC-101 (`scheduling_links` FOR SHARE / FOR UPDATE, 10 s `lock_timeout`):
  disjoint tables. We reuse its timeout value and its transaction scoping.
- PAC-71: the re-check's KEY SHARE is the first lock, exactly as today.
  Generation bumps (`UPDATE users SET session_generation`, FOR NO KEY UPDATE)
  do not conflict with KEY SHARE. Account deletion (`DELETE FROM users`, FOR
  UPDATE then cascade to the window rows) conflicts with KEY SHARE: it waits for
  the record transaction, or the record waits for it at step 1 holding nothing.
  Without step 1 the order would be window row → users (FK) against the
  deletion's users → window rows, a deadlock (challenge V-2, reproduced); step 1 is why the
  explicit lock is binding when no guard is present.
- Two record transactions of the same user hold at most one window row each,
  and of different users share nothing: no cycle.

### 3.6 How the operator sees it

```sql
-- windows where something was left out
SELECT user_id, window_start, window_start + interval '10 minutes' AS window_end,
       recorded, left_out, recorded + left_out AS denied_total, last_left_out_at
FROM sso_stepup_denial_windows
WHERE left_out > 0
ORDER BY window_start DESC;
```

`recorded` is the number of that user's denied outcomes recorded individually
for the window (the `sso_stepup` rows, whose own `created_at` is the database
clock and may differ from the window by clock skew); `left_out` the number not
recorded; `recorded + left_out` every denied outcome of that user in the
window. Granted outcomes are never in this table. No API reads it (as
`sso_events`, spec §5). Retention: none (out of scope, like `sso_events`); the
table grows by at most one row per user per window in which they were denied.

## 4. Budget answers of the callback (binding; PAC-95, spec D-8)

The budget's registered answer (`budget.OnExpiry`) moves through these values;
each registration replaces the previous one:

| Phase | Answer on expiry | Purpose parameter |
|---|---|---|
| From the start until check 0 completes | `denied&reason=state_mismatch` | none (state not verified) — unchanged |
| Check 1 running (exchange) | `token_invalid` | the state's — unchanged |
| Microsoft checks 2 running (organisation, pin) | `tid_mismatch` | unchanged |
| Check 4 running (admin lookup / roles) | `not_admin` | unchanged (OQ-2 = no) |
| **From the start of check 4b** (after check 4 passed) | **`unavailable`** | the state's |
| **Outcome decided, until step 5 starts** (connection reservation, `stepupBeforeCommit`, up to `budget.Commit`) | granted → **`unavailable`**; denied → **its own reason** | exactly what the decided redirect would carry (an unverifiable `state_mismatch` carries none) |
| Step 5 started (`budget.Commit` returned true) | none: waited for | — |

The registration for the decided outcome happens at the start of
`finishStepup`, before any call that can block. Concretely this fixes:
Google `no_hd` / `hd_mismatch` / `email_mismatch` (were answered `token_invalid`),
Microsoft `email_mismatch` / `tid_changed` (were `tid_mismatch`), an admin who
passed every check (was `not_admin`), check 4b (was `not_admin`), and the
early `token_invalid` (provider `error` / missing `code`, decided before any
`running` call, was answered `state_mismatch` with no purpose).

`unavailable` is never written to `sso_events` (it is only ever an expiry
answer, and an expiry answer records nothing). A budget answer never issues a
grant and never touches the denial window.

## 5. Web display contract (binding for the web PR)

Both `SsoAdminStepupSection` and `OrganizationAISection`:

1. **Reason lookup.** The reason text is found only for a reason that is one of
   the ten values in the `Location` pattern, by an own-key lookup (a `Map`, or
   `Object.hasOwn` on the table; never a bare `table[reason]` on an object with
   a prototype). Any other value of `reason` (absent, empty, `constructor`,
   `toString`, `valueOf`, `hasOwnProperty`, `__proto__`, `isPrototypeOf`,
   `something_new`, a future server value) renders exactly
   `Administrator confirmation was denied.` and no function text.
2. **`unavailable` text** (both tables):
   `the confirmation could not be completed in time, so nothing was granted. Start it again from this page.`
   (rendered after `Administrator confirmation was denied: `).
3. **`without_stepup` codes.** Same own-key rule; unknown codes are omitted (no
   text, no React child warning).
4. **Options failure (SSO section only; the org-AI section already shows it at
   `OrganizationAISection.tsx:394`).** The section is hidden only when the
   options request failed with HTTP 403 whose body parses as the generated
   `SsoWriteMembershipRequired` (`org membership required` or `org not found`,
   the two documented no-organisation answers; challenge C-1) and no
   step-up outcome is in the URL, or in a demo session (§5.5), or while the
   request is still loading with no outcome in the URL. On any other failure
   (another status including a 403 with another body, a network failure, a client timeout, a
   200 failing `SsoStepupOptionsSchema`) the section is shown with its title,
   the outcome if any, no entry point, and an error line
   (`data-testid="sso-stepup-options-error"`) whose text starts with
   `Administrator confirmation options could not be loaded.` followed by
   `ApiErrorText` with `effect="read"` for the error. A request cancelled
   because the page is being left (`ApiRequestCancelledError`) shows no line.
   No retry control (spec D-5).
5. **Demo session** (`isDemoSession()`): the SSO section renders nothing (no
   outcome, no entry point, no error line) and sends no options request
   (spec D-6, AC-22).
6. **Older and newer servers.** A web build with these tables against a server
   without PAC-75 never receives `unavailable` and behaves as today except for
   §5.1/§5.3/§5.4. A web build without PAC-75 against a PAC-75 server shows an
   `unavailable` landing as `Administrator confirmation was denied.` (its
   unknown-reason path), which is acceptable; so the web and api PRs can merge
   in either order (spec §0). The options request and its schema are unchanged.

**Type regeneration.** No generated type changes shape: no web type carries the
reason enum (the `Location` pattern is a header pattern, not a TS type). The
generated files embed descriptions, so `src/api/generated` drifts in comments
only (`types.ts`, `schemas.types.d.ts`); the web PR carries the regeneration
commit against this bundle so the cross-repo `openapi-check` passes. No
hand-written web code depends on the regenerated text.

## 6. Test seams (names may differ; behaviour may not)

- `stepupBeforeCommit` (exists, PAC-90): between reservation and `budget.Commit`.
  AC-25's second half and AC-27 use it.
- A seam before check 4b (new, e.g. `stepupBeforeIdentityCheck(ctx)`), so AC-26
  can expire the budget there.
- The connection reservation's expiry (AC-25 first half): a budget shorter than
  the pool wait, as `pac90_test.go:334-356` does.
- The window's clock is `SSOStepupOutcome.Now` (the handler's injected clock);
  AC-11 advances it past `window_start + 10 min`.
- AC-12 runs 50 concurrent `RecordSSOStepupOutcome` calls (and, separately, 50
  concurrent callbacks through the handler) on real PostgreSQL with at least two
  `*sql.DB` pools (two "processes").

## 7. Consumers

- `backend/api/pac90_test.go:334-356` (AC-5b): asserts `reason=unavailable`
  (intended tightening, spec AC-25).
- Backend tests that make more than 5 denied callbacks for one user within one
  window and count rows: none found (challenge, "Consumers affected"); the stage-4 PR re-greps.
- `backend/api/pac50_sso_gate_test.go:274` `failingSSOStore.recordStepup`:
  signature unchanged.
- `e2e/tests/sso-stepup.spec.ts:123-129` (one granted step-up then a write: two
  rows): unchanged (AC-15).
- Web: `SsoAdminStepupSection.tsx` / `OrganizationAISection.tsx` and their tests
  (§5); `SsoAdminStepupSection.test.tsx:122-127` (403 → empty) keeps passing
  with the documented body; `src/api/generated/**` comment drift.
- `mcp/`: no SSO call (grep: no `admin/sso`). Unaffected.
- `contracts/features/PAC-50.yaml` / `docs/specs/PAC-50.md` /
  `contracts/features/PAC-81-part-a.yaml` / `docs/factory/api-audit.md`: dated
  in-place notes (spec AC-24, AC-30, AC-31; D-7); no Challenge section altered.

## 8. Rollback

Revert the api PR (down 039 drops `sso_stepup_denial_windows`; individually
recorded `sso_events` rows are kept; only left-out counts are lost) and the web
PR, in either order (§5.6). The docs notes stay (they record history).

## 9. Alternatives rejected

1. **Count `sso_events` rows in the window under an advisory lock** (one class
   per user). Works, but needs a second lock primitive, a COUNT over the
   user's recent rows on every denial, and still a place for the left-out
   count. The upsert is the lock, the count and the operator record in one
   statement.
2. **Left-out counter inside the 5th `sso_events` row** (`details.left_out`
   updated in place). Mutates an audit row after the fact, and its
   `created_at` no longer dates what it counts. Rejected.
3. **A summary `sso_events` row per window** (new kind). Needs the kind CHECK
   changed again (031 already did once), and the "one summary per window"
   rule would itself need a partial unique index on JSON. More moving parts,
   same information as §3.6.
4. **An in-process rate limiter** (`ratelimit_detect.go` pattern). Fails
   AC-12 across replicas (spec challenge B4).
5. **A sliding window.** Exact sliding counts need either a row per outcome
   (the flood we are bounding) or a lock plus a range count. The fixed window
   costs at most 2× at a boundary, still bounded.
6. **A 429 on the callback.** Out of scope (spec §5): the web reads the 302.
7. **Reason name `try_again` or `budget_expired`.** `unavailable` (PAC-95's
   suggestion) names the state without exposing a server mechanism and reads
   naturally in operator logs; `try_again` is advice, not a reason.
8. **Normalising lookalike domains (NFKC / IDNA) instead of refusing.** Would
   accept fullwidth or U-label forms and store something the caller did not
   send; spec D-1 says refuse.
9. **Unicode lower-casing both sides (`strings.ToLower(a) == strings.ToLower(b)`).**
   Still folds the Kelvin sign and `İ` (spec challenge B3).

## 10. Gate results (contract stage)

Regenerated in an LF clone (`golang:1.25-bookworm`, oapi-codegen v2.4.1, the
api-gate pattern, host repo mounted read-only), 2026-11-14. Revision 1 at
`9b7d70b`: `make openapi WEB=/nonexistent` rc 0, regenerated
`contracts/openapi/openapi.yaml` and `backend/api/gen/paceday.gen.go` (doc
comments only). Revision 1.1 at `d9d82e5`: `make openapi WEB=/nonexistent` rc 0
(132 operations, 12 generated), regenerated `contracts/openapi/openapi.yaml`
only (committed `c238245`); `make openapi-check WEB=/nonexistent` rc 0 ("bundle
is in sync (110 paths)", "MIGRATION.md covers all 132 operations (12
generated)", "backend/api/gen matches the contract"; web half SKIPPED by
design); `cd backend && go build ./...` ok. Bundle sha256
`ae3d6c1c045e66fe898390ff291afa9655c53d1065b451135020738d6f2cef8d`
(= `contractHash`; main `fd4e7b1`: `85187c94…`). Not run: test suites, lint and
coverage (no hand-written Go changed).

**Web type regeneration: yes, comments only.** `src/api/generated/types.ts` and
`schemas.types.d.ts` embed the changed descriptions; no type, schema or enum
changes shape, so no hand-written web code depends on the regeneration. Not
run here (no web worktree); the web PR carries it.

## Challenge — 2026-11-14

**Verdict**: accept-with-changes. The changes are applied in revision 1.1 (this
document, the manifest, and the fragment text committed at `d9d82e5`, whose
bundle is `contractHash`).

The contract-challenger role was played by the same agent, which re-read
`origin/main` `fd4e7b1` (`handlers_sso.go`, `handlers_sso_stepup.go`,
`storage/sso_stepup.go`, `storage/session.go`, `internal/budget`, chi v5.4.0's
`mux.go`), the web at `4f71ad2` (both settings sections, `client.ts`,
`demoGate.ts`, generated `schemas.ts`), and ran every storage claim on a
throwaway PostgreSQL 16.15 container (`postgres:16`, `en_US.utf8`) on an
internal docker network with no published port, migrated with the real files
001..038 plus §3.2's 039, each in one transaction.

### Requests wrongly permitted

1. (C-2) `DELETE /api/admin/sso/%41cme.com` and `DELETE
   /api/admin/sso/%e2%84%aacme.com`: revision 1 said the segment is
   "percent-decoded". It is not always: chi v5.4.0 routes on `r.URL.RawPath`
   when set (`mux.go:460-466`), and Go sets RawPath whenever the request's
   escaping is not the canonical one, so `chi.URLParam` returns the escaped
   text (observed: `"%41cme.com"`, `"%e2%84%aacme.com"`; canonical
   `%E2%84%AAcme.com` gives `"Kcme.com"` with the Kelvin sign, `acme%C5%BF.com`
   gives `"acmeſ.com"`, `%EF%BD%81cme.com` gives `"ａcme.com"`). The outcome is
   a 403 either way (safe direction), but the contract described a decoding the
   server does not do. Fix (applied): deleteSsoProvider and setSsoProviderState
   describe the segment "as the router yields it", with both cases; AC-5 gains
   the two non-canonical requests.

### Responses the client cannot handle

1. (C-1) `getSsoStepupOptions` may answer 403 `{"error":"org not found"}` (the
   org row is gone; `auth.yaml` getSsoStepupOptions 403,
   `SsoWriteMembershipRequired` enum). Revision 1's §5.4 hid the section only
   for `org membership required`, so a user whose org was deleted would get
   an error line for a documented no-organisation answer. Fix (applied): hide
   on any 403 body that parses as the generated `SsoWriteMembershipRequired`
   (`schemas.ts:353`, already used that way at `client.ts:348`); AC-20/AC-21
   extended.
2. Nothing else: `unavailable` reaches an older web as an unknown reason, which
   (today, for a non-prototype name, and after §5.1 for every name) is the
   plain denial.

### Consumers affected

- `backend/api/pac90_test.go:334-356`: tightened (AC-25), intended.
- Event-counting tests (`api/pac50_sso_helpers_test.go:393` `events()` and its
  call sites in `pac50_rev81_test.go`, `pac50_sso_gate_test.go`,
  `pac50_sso_stepup_test.go`, `pac73p2_org_ai_test.go:467`,
  `pac90_test.go:285`; `pac73p2_rows_test.go:775`; `pac87_test.go:357`;
  `storage/sso_stepup_test.go`, `storage/org_ai_test.go`): each builds a fresh
  user per environment and makes a handful of callbacks for it at most; none
  found exceeding 5 denials for one user in one window. The stage-4 PR
  re-greps. `e2e/tests/sso-stepup.spec.ts` (two rows): unchanged.
- Web generated files: comment drift (§10). `mcp/`: no SSO call.

### Contract/implementation divergences (fixed in 1.1)

1. (C-3) Budget answer for a callback decided before any `running` call:
   `checkStepup` returns `token_invalid` for a provider `error=` or a missing
   `code` (`handlers_sso_stepup.go:575-577`) before registering anything, so
   on `main` the budget's answer in `finishStepup` is the initial
   `state_mismatch` **with no purpose** (`stepupCallback` registers it with
   purpose `""`, `:531`), even for a verified `llm_org_key_write` state.
   Revision 1's §4 already covers it (the decided redirect, purpose included);
   AC-27 now names this case explicitly.
2. (C-4) `info.description` exception (3) in revision 1 said "from the start of
   check 4b, and once the outcome is decided, a granted outcome answers
   `unavailable`", which reads as if check 4b's answer depended on a granted
   outcome. Fix (applied): "from the start of check 4b it is `unavailable`,
   and once the outcome is decided …".

### Uncertainties not actually resolved

- None. OQ-1/OQ-2 are factory defaults the owner may still change (two Go
  constants; a follow-up for OQ-2), not uncertainties about behaviour.

### Verified clean (PostgreSQL 16.15, `READ COMMITTED`)

- **V-1, race-freedom of the bound.** pgbench running §3.3's transaction
  verbatim (re-check KEY SHARE, `set_config` lock_timeout, the upsert with
  `RETURNING left_out \gset`, the event insert under `\if :left_out = 0`):
  50 clients × 1 transaction gave window row `recorded 5, left_out 45` and 5
  events; 50 clients × 4 transactions with `pg_sleep(0.02)` after the upsert
  (holders overlap) gave `5 / 195` and 5 events; 0 failed transactions, no
  40P01. The separate connections stand in for separate api processes:
  nothing outside the database takes part in the decision. "Recorded iff
  `left_out = 0`" matched the event count in both runs.
- **V-2, lock order.** Without §3.3 step 1 (update the window row, sleep, then
  insert the event) a concurrent `DELETE FROM users` of the same user
  deadlocks: `ERROR: deadlock detected … while deleting tuple … in relation
  "sso_stepup_denial_windows"` (reproduced). With step 1 (`SELECT 1 FROM users
  … FOR KEY SHARE` first) the same schedule completes: the delete waits for
  the record, then removes the user and its window row. This is why step 1 is
  binding when no session guard is present (AC-32). PAC-82's advisory classes,
  PAC-84's 8400, PAC-101's `scheduling_links` locks, grants and bookings are
  not touched by the record transaction, and a generation bump (FOR NO KEY
  UPDATE) is compatible with KEY SHARE, so no cycle with PAC-71, PAC-82 or
  PAC-101 exists; a granted record's tenant pin (FOR NO KEY UPDATE on
  `organizations`) is compatible with a concurrent denial's FK KEY SHARE.
- **V-3, wait bound.** A holder keeping the window row 2 s and a waiter with a
  1 s transaction-local `lock_timeout`: `ERROR: 55P03: canceling statement
  due to lock timeout` (AC-33).
- **V-4, `lower()` on org domains.** Under `en_US.utf8`, `lower('ıstanbul.com')`,
  `lower('straße.de')` and `lower('xn--bcher-kva.example')` are unchanged, so
  the unchanged storage predicates find a row stored under an org domain.
  Note: `lower(U&'\212Acme') = 'kcme'` is true in PostgreSQL too, so a Kelvin
  row would collide with `kcme.com` in the `lower(domain)` unique index; none
  can exist, because before PAC-75 the create stored `strings.ToLower`, which
  maps the Kelvin sign to `k`. Only `ſ` lookalikes can be stray rows (§2.3).
- **V-5, migration 039.** Applies after 001..038; an unaligned `window_start`
  (`10:00:01Z`, `10:00:00.5Z`) is refused by the CHECK; `11:00:00+01:00` is
  the same key as `10:00:00Z` (zone-independent); down drops only the table
  (`sso_events` 10 rows before and after); up again succeeds.
- **V-6, budget at the record step.** After `budget.Commit` returns true the
  budget cannot answer (`internal/budget/budget.go:73-85`), so a left-out or a
  recorded denial, and the `ErrTenantPinned` re-record as `tid_changed`, are
  all waited for; between the reservation and `Commit`, the registered answer
  is §4's decided one; a handler still running after expiry cannot record,
  because `Commit` refuses. A budget expiry during check 4b's binding read
  makes that read fail (`identity_mismatch` decided), but the answer already
  sent was `unavailable` and nothing is recorded. `OnExpiry` replaces the
  registration under the state's mutex, so the latest registration wins.
- **V-7, Unicode table (§2.1).** Every row follows from the bytewise rule:
  `ſ`, `K`, `İ`, `ı`, `ẞ` and fullwidth letters are multi-byte, so neither
  length nor bytes can match an ASCII letter; `ß` matches only itself; an
  A-label is ASCII; a U-label is a different byte string. Go's
  `strings.EqualFold` (today's create) accepts `ſ`/`s` and `K`/`k` and refuses
  `İ`/`i`; `strings.ToLower` (today's delete, before the comparison) maps `K`
  to `k` and `İ` to `i`, so today's delete accepts both; the new rule refuses
  both (AC-5, AC-6).
- **V-8, web on older servers.** An older server never sends `unavailable`;
  the options request and its schema are unchanged; the web's new behaviour
  (own-key lookups, error line) depends on nothing new from the server. A
  pre-PAC-75 web on a PAC-75 server: `DENIAL_TEXT['unavailable']` is
  `undefined`, so it renders the plain denial. Either merge order is safe.
- **V-9, conventions.** No `operationId`, schema name, `security` key or shared
  component changed; `make openapi-check` rc 0 (§10).
