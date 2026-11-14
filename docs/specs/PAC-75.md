# Spec — PAC-75: PAC-50 follow-ups (Unicode domain folding, step-up event flood, denial text, hidden options failure, contract wording) + PAC-95

- **Linear**: https://linear.app/paceday/issue/PAC-75 (folds in https://linear.app/paceday/issue/PAC-95)
- **Status**: accepted (2026-11-14, after the challenge below; no blocking finding open)
- **Author**: spec-author (factory)
- **Verified against**: clockwise-like `origin/main` fd4e7b1 (PAC-71, PAC-80, PAC-89, PAC-101 and PAC-82 merged); smart-calendar-flow `main` 4f71ad2
- **Inherited scope**: no `x-uncertain` marker on the six `/api/admin/sso*` operations (`contracts/openapi/paths/auth.yaml`, none in the file). Audit rows on touched endpoints: API-001 (status label only, see item 7), API-066 and API-091 (out of scope, §5), API-092 (no change needed; see §5).

Decision marks used below: **[F]** = assumed by the factory, pending owner confirmation; **[O]** = an owner decision already recorded (cited); **[OQ]** = needs the owner (§8).

## 0. Items and PAC-95

| # | Issue item | Still true on main? | Where | Size |
|---|---|---|---|---|
| 1 | Unicode case folding on the SSO write domain | **Yes**, line moved (`domain.go:64-66`, not ~328) | backend + contract wording | small |
| 2 | Unlimited `sso_events` rows from the step-up callback | **Yes, and wider than filed**: every denied outcome is recorded, not only `state_mismatch` | backend + contract | medium |
| 3 | `DENIAL_TEXT[reason]` prototype lookup | **Yes, in two components**, not one | web | small |
| 4 | Failed options request hides the SSO section | **Yes** (the org-AI section already shows its error) | web | small |
| 5 | PAC-50 contract wording (one vs three PAC-22 assertions) | **Yes**, both texts uncorrected | docs | trivial |
| 6 | PAC-95: budget denial at the record step says `not_admin` | **Yes**, and also mislabels decided denials | backend + contract + web | small |
| 7 | (touch-it-fix-it) API-001 status label stale | Yes | docs | trivial |

**PAC-95 is folded in [F].** Why: (a) it changes the same operation's outcome (`ssoAdminStepupCallback`) that item 2 must already re-describe in the contract ("every outcome … is recorded" stops being true), so one contract revision instead of two; (b) its web half is a new entry in the same two denial-text tables item 3 rewrites, so doing them separately means editing the same lookup twice and re-testing it twice; (c) items 2 and 6 both change what `finishStepup` does between the decision and the record, and specifying them apart invites two plans that each assume the other's code is unchanged. The cost is that PAC-75 now carries a contract change (one new redirect reason), which it needed anyway for items 1, 2 and 5. PAC-95 should be closed as "done in PAC-75" when this merges, not before.

**Split [F]:** none needed. Delivery is one stage-2 contract revision (items 1, 2, 5, 6 wording/enum), then one api PR (items 1, 2, 6 backend, 7) and one web PR (items 3, 4, 6 web), paired as usual. The web PR can merge before the api PR: until the api sends the new reason, the web never sees it. If stage 2 cannot settle item 2's operator record in one round, item 2 moves to its own ticket (Part B) and the rest ships; items 1, 3–7 do not depend on it.

## 1. Problem

PAC-50 put SSO configuration writes behind an administrator step-up. Its review left five non-blocking defects, and PAC-95 found a sixth:

1. An SSO write can be stored under a domain that only *looks* like the organisation's domain (it matches only under Unicode case folding). The row is stored, logged as a create, and then never shown in the organisation's SSO list, so an admin sees "nothing configured" while a stray row exists. Not a takeover (sign-in email domains are ASCII), but the stored state and the list disagree.
2. Any signed-in user can make the server write one operator-audit row per request, without limit, by hitting the step-up callback with a bad or refused state. That is unbounded table growth and makes the operator record useless when it matters (a real attack is buried).
3. The Settings page shows JavaScript source text (`function Object() { [native code] }`) as the "reason" for a denial when the URL's `reason` is the name of a built-in object property. The page is reachable by any link, so this is a cosmetic injection of nonsense into a security message.
4. When the step-up options request fails (server error, network), the SSO section silently disappears, exactly as it does for a user with no organisation. An admin who needs to change SSO sees nothing and has no clue anything is wrong.
5. PAC-50's contract understates how many PAC-22 test assertions it changed. Readers of the contract trust it as the record.
6. (PAC-95) When the server runs out of its request time budget after an administrator has passed every check, the admin is told "the account you used is not an administrator". That sends them to chase a permissions problem that does not exist. A decided denial hit by the budget is also mislabelled (with the label of an earlier check).

## 2. Current behaviour

### Item 1 — domain comparison

- `backend/domain/domain.go:64-66`: `DomainMatchesOrg(d, orgDomain)` is `d != "" && strings.EqualFold(d, orgDomain)`. `EqualFold` is Unicode simple case folding: `ſ` (U+017F) folds with `s`/`S`, `K` (U+212A Kelvin) with `k`/`K`.
- Create, `backend/api/handlers_sso.go:239-243`: step 3 calls `DomainMatchesOrg(body.Domain, caller.org.Domain)`; on success `body.Domain = strings.ToLower(body.Domain)` (Unicode lower-casing; `ToLower("acmeſ.com")` stays `acmeſ.com`). The row is saved under that value (`:271`) and recorded as `sso_provider_written` with `op: create`.
- Delete, `handlers_sso.go:465-469`: the `{domain}` segment is `strings.ToLower`-ed **before** the comparison, so `ToLower("Kcme.com")` = `kcme.com` passes for org `kcme.com` (harmless: it then targets the org's own row), and `acmeſ.com` passes for org `acmes.com` and deletes the stray row (today the only API way to remove one).
- List, `backend/storage/sso_providers.go:316-326`: `JOIN organizations o ON o.domain = s.domain` — byte equality, so a stray `acmeſ.com` row never appears.
- Org domains come from `storage/orgs.go:77-88` (`AssociateUserWithOrg` → `domain.ExtractDomain`, which `strings.ToLower`s, `domain.go:19-25`). They are Unicode-lower-case; in practice ASCII, but nothing enforces it.
- Tests: `domain/domain_test.go:40-66` covers ASCII cases only; no non-ASCII case exists anywhere.
- Contract wording, `contracts/openapi/paths/auth.yaml` createSsoProvider step 3 ("exact and case-insensitive", ~line 1410) and deleteSsoProvider ("lower-cased and not trimmed", ~line 1646): neither says which case-insensitivity.
- Other `EqualFold` domain comparisons, `handlers_sso_stepup.go:594` (Google `hd`) and `:651-657` (`containsFold`, Microsoft verified domains): the compared values come from the provider's signed token / Graph, not the caller; not this issue (§5).

### Item 2 — step-up event writes

- Route: `GET /api/admin/sso/stepup/callback` is mounted under `requireAuth` (`api/routes.go:206`, `handlers_sso_stepup.go:207`). No rate limiter applies (the only limiters are `ratelimit_detect.go` for detection and `ratelimit_booking.go`).
- `stepupCallback` (`handlers_sso_stepup.go:524-558`): a missing/invalid state → `finishStepup(..., reasonStateMismatch)` (`:546-548`); a valid state → `checkStepup` → `finishStepup` with the decided reason.
- `finishStepup` (`:664-700`) reserves a DB connection, then `recordStepup` → `storage.RecordSSOStepupOutcome` (`storage/sso_stepup.go:187-245`), which opens a transaction, runs the PAC-71 session re-check, and **always** inserts one `sso_stepup` row into `sso_events` (`:238`), granted or denied. Nothing counts or limits per user.
- So the floodable writes are: `state_mismatch` with no start at all (any signed-in user, org or not — `org_id` is NULL then, migration `030_sso_admin_stepup.up.sql:36-46`); and any other denial after a free `GET /api/admin/sso/stepup?provider=…` (which sets a cookie and writes nothing, `:460-521`) followed by a callback with `error=x` (→ `token_invalid`, `:575-577`). Only `granted` needs a real administrator.
- Contract, `auth.yaml` ssoAdminStepupCallback: "Every outcome with a session, granted or denied, is recorded server-side for operators (spec §4.6)". PAC-50 spec §4.6 (`docs/specs/PAC-50.md:162-170`) says the same.
- Tests reading the table: `e2e/tests/sso-stepup.spec.ts:123-129` (one granted step-up then a write: two rows), `backend/api/pac50_sso_helpers_test.go`, `pac71_migration_rules_test.go`, `pac73p2_rows_test.go`, `pac87_test.go`, `storage/sso_stepup_test.go`. Indexes: `idx_sso_events_user_created (user_id, created_at)` exists (`030…up.sql:48`).

### Item 3 — denial text lookup (web, smart-calendar-flow 4f71ad2)

- `src/components/settings/SsoAdminStepupSection.tsx:28-39` declares `DENIAL_TEXT: Record<string, string>` as a plain object literal; `:57` does `DENIAL_TEXT[params.get("reason") ?? ""]` and `:61` renders it if truthy. `reason=constructor` yields `Object`'s constructor function, rendered as its source text; `toString`, `valueOf`, `hasOwnProperty`, `__proto__` behave alike.
- **The same defect is in `src/components/settings/OrganizationAISection.tsx:47-58, 166-170`** (the `purpose=llm_org_key_write` landing, PAC-73 Part 2). The issue names only the first.
- Same pattern on server-supplied codes: `WITHOUT_STEPUP_TEXT[code]` (`SsoAdminStepupSection.tsx:22-25, 70`; `OrganizationAISection.tsx:41-44`). The codes come from our API, not the URL, so this is latent.
- Test: `SsoAdminStepupSection.test.tsx:159-163` covers `reason=something_new` only; no test uses a prototype key; no equivalent test in the org-AI section's tests.

### Item 4 — options failure

- `SsoAdminStepupSection.tsx:147-151`: `useQuery(... retry: false)`; `if (!options.data && !hasOutcome) return null`. `options.error` is never read, so 403 (no organisation), 5xx, network failure, client timeout and a schema-parse failure (`client.ts:299-302`, `SsoStepupOptionsSchema.parse`) all render nothing.
- The org-AI section already shows the error: `OrganizationAISection.tsx:394` (`ApiErrorText ... effect="read"`).
- The section is rendered for every signed-in user (`src/pages/Settings.tsx:816`), including a demo session (`src/api/demoGate.ts`: the options call has no demo implementation, so in a demo session it is a real request that fails; today that renders nothing).
- Test: `SsoAdminStepupSection.test.tsx:122-127` pins the 403 → empty; no test for 5xx or network failure.

### Item 5 — contract wording

- `contracts/features/PAC-50.yaml:255` (consumersAffected, "Revision 8.1 (round-6 N2)") says "only the assertion of the pre-PAC-50 403 … becomes 403 provider_domain_mismatch"; `docs/specs/PAC-50.md:345` §9 D-15(2) says "changing only the obsolete suffix-rule 403".
- `contracts/features/PAC-50.md:2745-2753` ("Challenge, revision 8.1") lists three. Verified on main in `backend/api/pac22_sso_secrets_test.go`: `:585` (403 `provider_domain_mismatch`), `:695-722` (case-variant secret keys → 400 `unknown property: <name>`), `:810-821` (upper-case domain replaces the lower-case row keeping its secret).

### Item 6 — PAC-95, budget at the record step

- `checkStepup` (`handlers_sso_stepup.go:572-649`) registers the budget's answer with `running(reason)`: `token_invalid` before the exchange (`:578`), `tid_mismatch` before the organisation lookup (`:619`), `not_admin` before the admin lookup (`:600`, `:635`). It is never updated after the admin check passes, so check 4b (`stepupIdentityMatches`, a DB read, `:604`, `:643`, `:745`) and the whole record step run under `not_admin`.
- Google denials decided before any `running` beyond `token_invalid` (`no_hd`, `hd_mismatch`, `email_mismatch`, `:590-598`) run the record step under `token_invalid`; Microsoft `email_mismatch`/`tid_changed` under `tid_mismatch`.
- `finishStepup` (`:676-689`): if acquiring the record's connection is cut by the budget (`r.Context().Err() != nil`) or `budget.Commit` refuses, it returns and the budget's registered redirect is sent: `/settings?stepup=denied&reason=not_admin` for an admin who passed everything. Nothing is recorded (PAC-90 AC-5b, `api/pac90_test.go:334-356`, asserts only "a denied redirect" and zero rows).
- The rule comes from PAC-81 Part A (`contracts/features/PAC-81-part-a.yaml:63`): "denied with state_mismatch before check 0 completes, else the running check's reason; step 5 (grant issue), once started, is waited for".
- The redirect enum is the `Location` pattern in `auth.yaml` ssoAdminStepupCallback 302 (nine values); the web knows the same nine (item 3 tables). No web generated type carries the enum.

### Item 7 — audit label

- `docs/factory/api-audit.md:259` API-001 is "Partly fixed (PAC-22, PAC-50)", while its own text says the disclosure half (PAC-22) and the write half (PAC-50) are both closed.

## 3. Desired behaviour

1. An SSO configuration write or delete is accepted for a domain only when that domain is the organisation's domain, letting ASCII letters differ in case and nothing else. Whatever is accepted is stored under exactly the organisation's domain, so every stored configuration is one the organisation's list shows. A domain that matches only through non-ASCII case folding is refused like any foreign domain.
2. A user cannot make the server write an unbounded number of step-up records. Per user, denied step-up outcomes are individually recorded up to a fixed bound per time window; beyond it they are not individually recorded, but the operator record still shows how many were left out. Granted outcomes are always recorded. What the user sees (the redirect and its reason) does not change.
3. On both Settings landings (SSO and organisation AI), a denial reason the page does not know — including one that is the name of a built-in object property — is shown as a plain denial with no detail. The same holds for "what you lose without it" codes.
4. The SSO section in Settings tells the user when it could not load the step-up options for any reason other than "you are not in an organisation"; it no longer vanishes silently.
5. PAC-50's contract and spec say three PAC-22 assertions changed, and which.
6. When the server cannot finish a step-up within its time budget after the administrator has passed every provider check, the landing says the confirmation could not be completed and should be tried again — not that they are not an administrator. When a denial was already decided, the landing gives that denial's reason. Nothing is granted or recorded in either case, as today.
7. The audit register's API-001 status matches its text.

## 4. Acceptance criteria

Test levels: `[unit]` Go or web unit/handler test; `[contract]` checked by the contract challenger / openapi checks at stage 2; `[e2e]` Playwright against the stack.

### Item 1

- **AC-1.** Given an org whose domain is `acmes.com` and a caller with a live grant, when they create a configuration for `acmeſ.com` (U+017F), then the answer is the existing foreign-domain refusal (403 `provider_domain_mismatch`, owner 2026-10-27 [O]), the grant is not consumed, no configuration row exists for any domain afterwards, and no write is recorded. `[unit]`
- **AC-2.** Given an org whose domain is `kcme.com`, when a caller with a live grant creates a configuration for `Kcme.com` (Kelvin sign), then it is refused exactly as AC-1. `[unit]` (Fails today: the fold accepts it and stores `kcme.com`.)
- **AC-3.** Given an org whose domain is `acme.com`, when a caller with a live grant creates for `ACME.com`, then it is accepted, and the stored configuration's domain is byte-identical to the org's domain and appears in the org's list. `[unit]` (Regression guard; PAC-22's `pac22_sso_secrets_test.go:810-821` must keep passing.)
- **AC-4.** For every accepted create (property: over the domain-matching unit table, including mixed ASCII case), the domain stored equals the org domain byte for byte. `[unit]`
- **AC-5.** Given org `acmes.com` (or `kcme.com`), when a caller deletes `acmeſ.com` (or `%E2%84%AAcme.com`), then the answer is 403 `provider_domain_mismatch`, no grant is consumed and no row is removed. `[unit]` (Fails today for both.)
- **AC-6.** The domain-matching unit table gains: U+017F, U+212A, a full-width letter, a dotless/dotted-i pair (`ı`, `İ`), and an org domain containing a non-ASCII letter matched by its exact bytes (accepted) and by its non-ASCII upper case (refused). `[unit]`
- **AC-7.** The createSsoProvider and deleteSsoProvider descriptions say "case-insensitive for ASCII letters only; any other difference, including non-ASCII case folding, is a different domain". `[contract]`

### Item 2

Bound and window **[F]** (D-3): at most **5** denied step-up outcomes recorded individually per user per **10-minute** window (the state cookie's lifetime). Numbers are the factory's; the AC hold for whatever the contract fixes.

- **AC-8.** Given a signed-in user with no step-up in the window, when they make 50 callbacks that each end denied (any mix of reasons, with and without a start), then each of the 50 answers is the same redirect, with the same reason, as today; at most 5 denied outcomes for that user are individually recorded in that window; and the operator record shows that 45 were not individually recorded for that user and window. `[unit]` (Fails today: 50 rows.)
- **AC-9.** Given the bound reached for a user, when that user then completes a step-up that is granted, then the grant is issued and the granted outcome is recorded exactly as today. `[unit]`
- **AC-10.** Given user A at the bound, when user B (same org) is denied, then B's outcome is recorded individually. `[unit]`
- **AC-11.** Given a user at the bound, when the window has passed, then their next denied outcome is recorded individually again. `[unit]` (clock injected)
- **AC-12.** Given 50 denied callbacks by one user issued concurrently, then no more than 5 are recorded individually and the left-out count is 45. The bound holds whatever the number of api processes, because it is decided where the records are kept. `[unit]` (DB-backed)
- **AC-13.** An orgless user's `state_mismatch` callbacks are bounded the same way. `[unit]`
- **AC-14.** The contract no longer says every outcome is recorded; it states the bound, the window, what is left out, how the operator sees the left-out count, and that the user-visible redirect is unaffected. PAC-50 spec §4.6 gets the same note. `[contract]`
- **AC-15.** The existing e2e journey (`e2e/tests/sso-stepup.spec.ts`, granted step-up then a write: two rows) passes unchanged. `[e2e]`

### Item 3

- **AC-16.** On the SSO landing, for each of `constructor`, `toString`, `valueOf`, `hasOwnProperty`, `__proto__`, `isPrototypeOf`, the page states "Administrator confirmation was denied." with no detail and renders no function text. `[unit]` (Fails today for at least `constructor`.)
- **AC-17.** The same on the organisation-AI landing (`&purpose=llm_org_key_write`). `[unit]`
- **AC-18.** On both sections, an options answer whose "without step-up" list contains `constructor` (or another prototype key) shows only the known codes' texts, and renders no function text and no React child warning. `[unit]`
- **AC-19.** Every known reason (the nine of today plus item 6's new one) still shows its own text on both landings. `[unit]`

### Item 4

- **AC-20.** Given a signed-in user whose options request answers 500 (and, separately, a network failure, a client timeout, and a 200 that fails schema validation), when Settings is shown, then the SSO section is shown with a visible error line saying the administrator confirmation options could not be loaded, and no entry point. `[unit]` (Fails today: empty.)
- **AC-21.** Given the options request answers the no-organisation 403, the section is not shown (unchanged; `SsoAdminStepupSection.test.tsx:122-127` keeps passing). `[unit]`
- **AC-22.** Given a demo session, the section is not shown and no error line appears (unchanged) **[F]** (D-6). `[unit]`
- **AC-23.** Given a step-up outcome in the URL and a failing options request, both the outcome and the error line are shown. `[unit]`

### Item 5

- **AC-24.** `contracts/features/PAC-50.yaml` (consumersAffected, the Revision 8.1 N2 entry) and `docs/specs/PAC-50.md` §9 D-15(2) name the three changed assertions (domain 403 → `provider_domain_mismatch`; case-variant secret keys → 400 `unknown property: <name>`; upper-case domain replaces the lower-case row keeping its secret), each with a dated "corrected by PAC-75" note; no Challenge section is altered. `[contract]`

### Item 6 (PAC-95)

- **AC-25.** Given a step-up that passes every check (Google and Microsoft, each), when the budget expires before the record starts (at the connection reservation, and at the commit — PAC-90's seam), then the redirect carries a new denial reason meaning "could not be completed, try again", distinct from every existing reason; no grant is issued and nothing is recorded. `[unit]` (Fails today: `not_admin`.) PAC-90 AC-5b's test is tightened to assert this reason.
- **AC-26.** Given a step-up whose administrator check passed, when the budget expires during the identity-binding check (PAC-71 4b), then the redirect carries the same "try again" reason. `[unit]`
- **AC-27.** Given a denial already decided (e.g. Google `hd_mismatch`, Microsoft `email_mismatch`), when the budget expires before its record starts, then the redirect carries that decided reason (not `token_invalid` / `tid_mismatch`) and nothing is recorded. `[unit]`
- **AC-28.** While checks 0–4 are still running, the budget's answer is PAC-81's rule unchanged (`state_mismatch` before check 0, else the running check's reason). `[unit]` (Regression guard; see OQ-2.)
- **AC-29.** Both Settings landings state the new reason in plain words: the confirmation could not be completed, nothing was granted, try again. `[unit]`
- **AC-30.** The callback's `Location` pattern and description list ten values; the description says the new one is never recorded (it only arises when the record did not happen) and why the redirect enum and the recorded enum now differ by that one value. PAC-81 Part A's rule text gains the post-check-4 case. `[contract]`

### Item 7

- **AC-31.** API-001's status in `docs/factory/api-audit.md` is "Fixed (PAC-22, PAC-50)", with a one-line note that GET still answers any member, by design since PAC-22 (no secret in it). `[contract]` (doc check by the reviewer)

## 5. Explicitly out of scope

- `EqualFold` on provider-asserted domains (`handlers_sso_stepup.go:594`, `:651-657`): the values are signed by Google or come from Microsoft Graph's verified-domain list, which requires DNS proof (ASCII or punycode). Not caller-controlled. Not changed.
- `EqualFold` on emails (`handlers_teams.go:321`, `handlers_manager.go:713`): emails, not SSO domains; different issue. Not filed here because both are also gated by a confirmed-address or user-id check; the reviewer may raise it.
- Removing stray rows already stored under a lookalike domain. No automatic deletion **[F]** (D-2): deleting SSO configuration is destructive and the rows are inert for sign-in. After this change the API can no longer delete them (AC-5), so the release note carries a read-only operator query listing SSO rows whose domain is no organisation's domain, and says how to remove them by hand.
- Rate-limiting the callback with a 429, or limiting the step-up start: the contract makes every callback outcome a 302 to Settings and the web reads it; the start writes nothing to the database.
- Retention or pruning of `sso_events`; an operator view of it (PAC-50 §4.6 keeps that a separate issue).
- Replacing PAC-81's "running check's reason" rule for budget expiry *during* checks 0–4 (OQ-2).
- API-066 (no route can suspend a configuration): PAC-50 deliberately serves no suspend/resume; it is a feature, not a fix of anything touched here. API-091 (no web consumer of `/api/admin/sso` writes): the SSO admin page is PAC-50's named follow-up. API-092: its `/api/admin/sso` share is already covered by PAC-22/PAC-50 tests.
- A retry button in the SSO section on options failure: reloading Settings retries **[F]** (D-5).

## 6. What must not change

- ASCII mixed-case domains keep matching and are stored lower-case (`domain_test.go:40-66`; `pac22_sso_secrets_test.go:810-821`).
- Subdomain and foreign-domain refusals stay 403 `provider_domain_mismatch` and consume no grant (`pac50_sso_gate_test.go`, owner 2026-10-27).
- Every callback outcome stays a 302 with today's Location for today's cases, and clears the state cookie (`pac50_*_test.go`, `e2e/tests/sso-stepup.spec.ts`).
- Granted outcomes: recorded, tenant pinned, grant issued in one transaction (PAC-50 D-4; `storage/sso_stepup_test.go`).
- PAC-90 AC-5b / AC-6: nothing stored when the budget answers first; the reserved connection is released (`pac90_test.go:334-370`). Only the asserted reason tightens.
- PAC-81: `state_mismatch` before check 0 at the budget (`PAC-81-part-a.yaml:103`).
- The org-AI landing stays separate from the SSO landing (`isSsoStepupLanding` / `isOrgAIStepupLanding`, `src/lib/orgAI.ts:12-21`; `orgAI.test.ts`).
- The no-organisation 403 shows no SSO section (`SsoAdminStepupSection.test.tsx:122-127`).

## 7. Rollback

Revert the api and web PRs. Item 2's operator-record change may need a migration (decided at stage 2); if it adds a column or table, its down step drops only what it added and loses only the left-out counts, never an individually recorded outcome. Item 6's new reason: a web build without it shows such a denial without detail (AC-16's path), so the two halves can be reverted in either order. Items 5 and 7 are text.

## 8. Open questions

- **OQ-1 (owner, non-blocking; factory default D-3 applies):** is "5 individually recorded denials per user per 10 minutes, the rest counted" the right bound? Resolve: owner reply on PAC-75.
- **OQ-2 (owner, non-blocking; default: no):** should a budget expiry *while* a provider check is running (e.g. Google's admin lookup timing out) also give the new "try again" reason instead of that check's reason? Today and after this spec it says e.g. `not_admin`. Changing it reverses part of PAC-81 Part A's decided rule. Resolve: owner reply; if yes, a follow-up ticket.
- **OQ-3 (stage 2, contract-author):** the name of the new reason (PAC-95 suggests `unavailable`) and how the operator record carries the left-out count (a counter on a row vs a summary row vs elsewhere). The spec fixes only the observable outcome.

## 9. Decisions

| ID | Decision | Mark |
|---|---|---|
| D-0 | Fold PAC-95 into PAC-75 (reasons in §0) | [F] |
| D-1 | Item 1: match = equal after ASCII-only case folding; refuse (not normalise) anything else; refusal is the existing 403 | [F]; the 403 status is [O] (owner 2026-10-27, PAC-50) |
| D-2 | No automatic cleanup of existing lookalike rows; release-note operator query | [F] |
| D-3 | Item 2: bound denied records (5 per user per 10 min), always record grants, count the rest; enforced where the records live so it holds across processes and under concurrency; no 429 | [F] |
| D-4 | Item 3: fix both landings and the "without step-up" lookup too (touch-it-fix-it) | [F] |
| D-5 | Item 4: an error line only, no retry control | [F] |
| D-6 | Item 4: demo session keeps showing nothing | [F] |
| D-7 | Item 5: correct in place with a dated note; Challenge sections untouched | [F] |
| D-8 | Item 6: after check 4 passes, the budget answers "try again"; for a decided denial, that denial; during checks 0–4, PAC-81's rule unchanged | [F] (PAC-81's rule itself is a recorded factory decision, not changed) |
| D-9 | Item 7: relabel API-001 Fixed | [F] |
| D-10 | No split; one contract revision, paired api + web PRs; item 2 may detach to its own ticket if stage 2 stalls on it | [F] |

## Challenge — 2026-11-14

Run by the factory as spec-challenger on the draft above (same session; no verbal gloss used — every check was redone from the code). The draft was amended for each finding; the amendment is noted.

**Verdict**: accept (after amendments; nothing blocking remains)

### Blocking (resolved in the body)
1. **Item 2 was scoped to `state_mismatch` only** (as the issue says). Any denial can be looped: a start writes nothing and a callback with `error=x` is `token_invalid` (`handlers_sso_stepup.go:575-577`). A fix that bounds only `state_mismatch` leaves the flood open. Resolved: §2 item 2 and AC-8 cover every denied reason, with and without a start.
2. **Item 3 named one component; the defect is in two.** `OrganizationAISection.tsx:47-58, 166-170` has the identical lookup on the org-AI landing. Resolved: AC-17.
3. **Item 1's fix as written in the issue ("compare `strings.ToLower(d) == org.Domain`") would not fix delete for the Kelvin sign**, because delete lower-cases before comparing (`handlers_sso.go:465`) and `ToLower(U+212A)` is `k`. The draft's first AC set only tested create. Resolved: AC-2 and AC-5 test both characters on both operations, and D-1 states the rule as "refuse", not "normalise".
4. **Draft AC-8 said "at most 5 rows" without saying the bound must hold concurrently or across processes.** A per-process in-memory limiter (the codebase's only existing pattern, `ratelimit_detect.go`) would pass a sequential test and still allow N×5 rows with N replicas or a parallel burst. Resolved: AC-12 and D-3.

### Non-blocking
1. Item 6 also mislabels decided denials (Google `hd_mismatch` reported as `token_invalid` at the record step), which PAC-95 does not mention. Added as AC-27.
2. Item 6's check 4b runs under the `not_admin` label even though PAC-81's own rule would call it `identity_mismatch`. Both are wrong for a server delay; D-8 picks "try again" (AC-26). Recorded as a deviation from PAC-81's literal rule.
3. Item 4 must not start showing an error in a demo session (the options call has no demo implementation). Added AC-22, D-6.
4. After item 1, existing lookalike rows become undeletable through the API. Recorded in §5 and D-2 with an operator query; owner may prefer a cleanup migration (not raised as an OQ because the rows are inert).
5. `WITHOUT_STEPUP_TEXT[code]` has the same latent defect on server-supplied input. Added AC-18 (D-4).
6. The PAC-50 spec §4.6 and the callback contract both say the redirect enum *is* the recorded enum; the new reason breaks that. AC-30 requires saying so.
7. API-001's stale label is on a touched endpoint (touch-it-fix-it). Added as item 7.
8. §4–§5 name existing files, test files and the existing 403 error string. The spec-author rule forbids naming interfaces in §3–§5; these name only *existing* artefacts used as regression anchors or doc targets (item 5 and 7 are about specific documents), and introduce no new endpoint, field or table. Left as is; the new reason's name and the operator-record shape are deliberately left to stage 2 (OQ-3).

### Criteria I could not falsify
- None after amendment. (Draft AC-14 said the contract "documents the change"; rewritten to list what it must state.)

### Citations I checked and found wrong
- Issue: "`backend/domain/domain.go` (~328)" — actually `domain.go:64-66` on fd4e7b1 (the file is 120 lines).
- Issue: "`handlers_sso_stepup.go` ~463" — the `state_mismatch` record is reached through `stepupCallback` `:546-548` → `finishStepup` `:664` → `storage/sso_stepup.go:238`.
- PAC-95: "`api/handlers_sso_stepup.go:648-674`" — `finishStepup` is now `:664-700` (PAC-89/PAC-80 moved it).

### Consumers this breaks
- `backend/api/pac90_test.go:334-356` — must assert the new reason (intended tightening).
- `smart-calendar-flow/src/components/settings/SsoAdminStepupSection.test.tsx`, `OrganizationAISection` tests — gain cases; none of today's assertions change.
- `backend/api/pac50_*_test.go`, `pac71_migration_rules_test.go`, `pac73p2_rows_test.go`, `pac87_test.go`, `storage/sso_stepup_test.go`: any test that makes more than 5 denied callbacks for one user within 10 minutes and counts rows would break; the plan must grep and confirm (none found by name in this review, but not every test body was read).
- No web generated type carries the reason enum (searched `not_admin` in smart-calendar-flow: only the two components and their tests), so the new value needs no regeneration.
