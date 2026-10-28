# Plan — PAC-71: bind accounts to the provider identity, not the email (backend)

- **Spec**: `docs/specs/PAC-71.md` revision 4, accepted (challenge revision 4: accept).
- **Contract**: `contracts/features/PAC-71.{yaml,md}` revision 2, accepted (challenge revision 2: accept),
  `contractHash` `5d54c76194180202d11049885b711f49f647a386baded03eae53e57481f4abb9`.
- **Branch**: `impl/pac-71-identity-binding-backend`, cut from `origin/factory/pac-71-contract`
  (`4eed1de`) with `origin/main` (`ae5c2cf`) merged (already contained).
- **Scope of this PR**: the backend stream only (`backend/`, `e2e/auth.ts` and its seed, docs).
  The web stream (`smart-calendar-flow`) is a separate PR after this one.
- **Provider facts**: merge condition 2 cannot be satisfied by an agent. What to run and record is
  in `docs/plans/PAC-71-provider-verification.md`. That condition stays **OPEN** in this PR.

## 1. Order of work

1. Migration `032_identity_binding` (next free number: main's latest is `031_llm_org_ai`, PAC-73 part 2).
2. Session generation: JWT `gen` claim, `requireAuth` lease and uncached read, 401/500 split,
   the response guard that turns a stale in-request re-check into the 401.
3. In-request re-check primitives (storage) and the D1–D14 load sites (§4).
4. Identity resolution and binding (storage), then the three sign-in callbacks.
5. Connect starts (generated strict interface) and the callbacks' connect branch.
6. Address standing at the granting, revoking and keeping sites; break-glass; step-up check 4b.
7. Teams 400 texts; `addressStanding` on `/api/auth/me`.
8. Operator command `backend/cmd/paceday-operator` (free address, reassign, advance all).
9. e2e session helper carries `gen`; the seed gives the journeys' users domain-confirmed standing.
10. Tests for every AC (§6), the consumer amendments (§7), then build, vet, lint, full suite, coverage.

## 2. Migration `032_identity_binding`

Up:

| Object | Shape | Why |
|---|---|---|
| `users.session_generation` | `BIGINT NOT NULL DEFAULT 0 CHECK (>= 0)` | Spec §3 "Sessions". Every existing account, and every row the pre-PAC-71 API still inserts during the deploy window (contract §14 step 1, challenge r2 NB3), starts at 0, so every existing session stays valid until the account binds or the operator advances it. |
| `users.address_standing` | `TEXT NOT NULL DEFAULT 'unconfirmed' CHECK IN ('domain_confirmed','unconfirmed')` | Spec §3 "Evidence and address standing". Unbound legacy rows are unconfirmed. |
| `users.email_key` | `TEXT NULL`, `UNIQUE` index | Case-insensitive uniqueness for every account created from now on (spec D-17, AC-7 `Zoe@`/`zoe@`). Set to `lower(email)` for every existing row whose lower-cased address is unique; left NULL for existing case collisions (spec: neither merged nor deleted) and for rows the old API inserts during the deploy window. NULLs never conflict. |
| `users (lower(email))` index | plain | Every address lookup is now `lower(email) = lower($1)`. |
| `user_identities` | `user_id UUID PRIMARY KEY REFERENCES users ON DELETE CASCADE`, `provider TEXT CHECK IN ('google','microsoft','oidc')`, `tenant TEXT NOT NULL DEFAULT ''`, `subject TEXT NOT NULL CHECK (<> '')`, `evidence TEXT CHECK IN ('authoritative','mailbox')`, `bound_by TEXT CHECK IN ('created','rule_a','rule_b','operator')`, `bound_at`; `UNIQUE (provider, tenant, subject)` | The identity is (authority, subject): Google `('google','',id)`; Microsoft work `('microsoft', lower(tid), lower(oid))`; SSO `('oidc', iss, sub)`. The primary key on `user_id` is "at most one bound identity per account" (spec §3); the unique triple is "one account per identity". |
| `operator_audit` | `CREATE TABLE IF NOT EXISTS`, no foreign key to `users` | Spec §3 "Operator procedure": the record "survives later changes to the account". |

Down drops `user_identities`, the three `users` columns and the two indexes, and **keeps
`operator_audit`** (spec §7, contract `rollback`). Up is idempotent for that table
(`IF NOT EXISTS`), so up → down → up works. No row of any table is deleted or rewritten except
the new columns (AC-18). The migration writes no binding.

## 3. Sessions: `gen`, requireAuth, and the request lease

- `auth.Claims` gains `gen` as a raw JSON value. Absent → 0. Present → must be a JSON integer
  literal `^[0-9]+$` within int64; anything else (`"0"`, `null`, `-1`, `1.5`) makes the token
  invalid (CONTRACT-4). `auth.GenerateToken` (3 args) keeps minting `gen` 0;
  `auth.GenerateSessionToken(id, email, gen, secret)` is what `issueJWT` uses.
- `requireAuth(db, secret)`: after the signature, expiry and `sub` checks it opens the **request
  lease** (`storage.BeginSessionLease`): a transaction on its own connection that takes
  `pg_advisory_xact_lock_shared(<PAC-71 class>, hashtext(user_id))` and reads
  `users.session_generation` uncached. Outcomes:
  - read error → 500 `application/json` `{"error":"internal error"}`, no Set-Cookie (AC-48);
  - row present, `gen` ≠ current → today's 401 bytes (AC-42);
  - no row → the request proceeds as today (contract §5), with a guard that makes every
    in-request re-check answer 401 (CONTRACT-12);
  - otherwise the lease is held until the handler returns, then rolled back.
  The lease uses `context.WithoutCancel`, so a client that disconnects cannot end it while the
  handler still runs.
- **Every generation advance** (binding, operator free/reassign) takes the same advisory lock
  **exclusively**, then `SELECT … FOR UPDATE` on the account row. So no advance can commit while
  a request authenticated under the previous generation is in flight, and a request that arrives
  while an advance waits queues behind it and then sees the new generation (401). This is the
  mechanism that makes "an older-generation request never reaches what a binding stored" hold
  for **every** D1–D14 read and write and every provider call a request makes, including the
  ones no primitive below covers (the whole handler runs inside the lease).
- **In-request re-check (literal, contract §5).** `storage.SessionGuard` (user, `gen`, stale and
  unavailable flags) rides on the request context. `storage.WithAccountGeneration(ctx, db, user, fn)`
  runs `fn` in one transaction whose first statement is
  `SELECT session_generation FROM users WHERE id = $1 FOR KEY SHARE`; a missing row or a different
  value marks the guard stale and returns `storage.ErrStaleSession` without running `fn`; a read
  error marks it unavailable. It uses a row lock, not the advisory lock, so it never queues behind
  an advance that is itself waiting for this request's lease (no cross-connection deadlock).
  When the context carries no guard for that user (scheduled jobs, other accounts' data, tests
  that inject a user without requireAuth) it runs `fn` directly.
- **Response guard.** `requireAuth` wraps the `ResponseWriter`: at the first `WriteHeader`/`Write`,
  or when the handler returns without writing, a stale guard replaces the response with today's
  401 bytes and an unavailable one with the JSON 500, dropping every header the handler set
  (no Set-Cookie leaks). That is "the operation answers the same 401, whatever it had already
  decided" (contract `bearerAuth`).
- **Why both mechanisms.** The lease alone already prevents the interleaving that AC-46/47/50
  describe; the literal re-check is what the merge condition names for the listed loads, and it
  is what answers a request whose account row is absent (CONTRACT-12). The seam tests (§6)
  exercise both: with the lease (the binding waits; the request gets nothing of Henry's) and at
  the primitive (a binding committed between authentication and the load: 401, no provider call).
  **Deviation to review:** with the lease, the AC-47 write half ("authenticated before the
  binding, write attempted after") cannot occur: the binding waits for the request, sees its
  write in the re-check, and is refused (the AC-46 outcome). AC-47's stated end state (bound,
  write refused) is proven at the primitive level for the listed write kinds instead. Both
  orders are safe; the lease is strictly stronger. Flagged in the PR for the reviewer.

## 4. D1–D14 load sites (contract §5) and what guards each

All of them run inside the request lease. The ones below additionally carry the literal
in-statement/in-transaction re-check (`…Ctx` variants), at every call site on a request path:

| Primitive (new `Ctx` variant) | Call sites switched |
|---|---|
| `auth.LoadUserTokenCtx` (D1) | `handlers_auth.go` status; `handlers_conferencing.go` addEventConference; `engine.LoadCalendarTokenCtx` (→ `handlers_calendar.go` listEvents/freeBusy, `handlers_events.go`, `engine/cal_iface.go newCalOps` = SmartScheduler suggest/create, NLP confirm, focus, compression; `engine/analytics.go`, `engine/manager.go` detect); `engine/habits.go`; `engine/freebusy_service.go` (requester's own); `engine/team_availability.go` requester's own row only |
| `auth.LoadMicrosoftTokenCtx` (D2) | `handlers_auth.go` status; `handlers_conferencing.go` createConferenceLink/addEventConference (teams); `engine/freebusy_service.go` outlook path; `credential_lifecycle.go microsoftStore.load` |
| `storage.LoadZoomCredential` via `credential_lifecycle.go zoomStore.load` (D2) | Zoom meeting creation, disconnectZoom (store carries the request context) |
| `storage.GetWorkspaceConnectionCtx` (D6) | `handlers_daily_recap.go:159`, `handlers_integrations.go:98,230`, `engine/meeting_brief_service.go:66,71` |
| `storage.ListPersonalCalendarsByUserCtx`, `GetPersonalCalendarCtx` (D7) | `handlers_personal.go:53,146,199`; `engine/personal_blocker.go:52,68,76` |
| `storage.GetOrCreateSettingsByUserCtx` (D2–D5) | every request-path caller in `api/`, `engine/` and `nlp/parser.go` listed in contract §5 |

Writes with the literal re-check (the kinds AC-46/47 name, plus every credential store):
settings save (`SaveSettingsForUserCtx`: AI keys, every preference), Slack/Notion connection
save, team creation, co-host acceptance, Google/Microsoft credential store on connect and on
sign-in. Every other D1–D14 write is covered by the lease only.

Not covered (no session account, contract §5): other members' credentials in team availability,
the public booking pages, scheduled jobs.

## 5. Sign-in, binding and connect

`storage.ResolveSignIn` is the one implementation of spec §3 "The sign-in rule" and the
contract's SIGN-IN step 4 for all three callbacks:

1. **Bound identity** (`user_identities` by provider, tenant, subject): one transaction locks the
   account row (`FOR NO KEY UPDATE`), re-reads the binding, overwrites name/picture as today,
   stores the credential, reads the generation for the session. Stored address and standing are
   never changed (D-7).
2. **Carriers** of the address, `lower(email)`: two or more → refused (case collision, logged);
   one bound → refused; one unbound and the provider is SSO → refused (D-10).
3. **Unbound legacy carrier** (Google, Microsoft): first check of rule (a) (recorded
   `provider_id` in the provider's format — Google `^[0-9]{1,255}$`, Microsoft GUID compared
   case-insensitively — equal to the subject, evidence ≥ mailbox for Google, authoritative for
   Microsoft) or rule (b) (authoritative and **empty**). Neither → refused, the log names the
   account and the first non-empty kind. Then the **seam** (`SignInHooks.BeforeStore`), then the
   binding transaction: advisory lock exclusive (with `lock_timeout`), row `FOR UPDATE`,
   re-check everything (still the only carrier, still unbound, (a)'s record unchanged or (b)'s
   emptiness), insert the identity, set standing and `provider_id = subject`, advance the
   generation, store the credential (Microsoft: without touching `calendar_provider`), commit.
   A failed re-check is the refusal. (a) on mailbox evidence for a non-consumer domain is logged
   for operator review (D-25).
4. **No carrier**: none → refused; otherwise the seam, then one transaction inserts the account
   (`email_key = lower(email)`, standing from the evidence, `provider` = first sign-in,
   `provider_id = subject`, generation 0), the identity and the credential. A unique violation
   (`email_key`, `email`, or the identity) means a concurrent creation won: refused.

**Emptiness** (`storage.FirstNonEmptyKind`) checks every kind of spec §2 / contract §5–§6, in
the contract's order D1…D14, against the account, and the settings row column by column against
contract §6 (decoded JSON, schedules after `normalizeSchedules`, `recap_send_time` as `HH:MM`;
credentials and keys must be empty or NULL; an unnamed column counts as changed). It then sweeps
`pg_constraint` for every foreign key to `users(id)` that the explicit list does not name and
counts a row there as non-empty (`unclassified:<table>`), so a later migration fails closed at
run time; a test fails the build-time check too (CONTRACT-5).

**Callbacks.** Google: evidence from `verified_email` and `hd` (§3 of the contract). Microsoft:
`/me`, then `GET /v1.0/organization?$select=id,verifiedDomains` with the same token (new seam
`microsoftGraphOrganizationURL`); work account only for a 200 with exactly one entry, GUID ids;
anything else is the refusal. SSO: `(oidc, iss, sub)`, `email_verified=false` refused, any
carrier refused. The refusal is `302 <FRONTEND_URL>/?error=signin_refused`, never touching
`auth_token`. Sessions carry the account's current generation.

**Connect.** `POST /api/auth/{google,microsoft}/connect` through the generated strict interface
(MIGRATION.md rows flip to `generated`). State = `base64url(payload).HMAC-SHA256` keyed from the
session signing secret with a PAC-71 label; the payload names user, `gen`, provider, expiry
(300 s) and a nonce; set as `google_connect_state` / `ms_connect_state` (HttpOnly, Lax, 300 s,
Secure by the deployment rule). The callbacks pick the branch only by which state cookie equals
`state`, then run the contract's steps 1–7; the store is one `WithGeneration` transaction that
re-reads the generation under the row lock (seam `connectBeforeStoreHook` between the step-3
check and the store, AC-47). Every connect-branch response expires the connect cookie.

## 6. Acceptance criteria → tests

Every test below drives the real handlers with the existing fake-provider seams (offline).
File names are in the PR body's table. Interleaving seams: `storage.SignInHooks.BeforeStore`
(AC-7, AC-7b, AC-46), `api.afterAuthLeaseHook` (AC-47, AC-50 lease side),
`api.connectBeforeStoreHook` (AC-47 connect), the primitive-level guard injection
(AC-47/AC-50 literal side, CONTRACT-12), and a `getTeam` seam (CONTRACT-9).

AC-23 (e2e sign-in stand-in) is **dropped** with OQ-4's reason: e2e cannot reach the callbacks
(`e2e/auth.ts`), so AC-1 to AC-7b, which drive the real callback handlers, are the proof.
AC-44 is implemented as the operator command `advance-all` and tested; whether a release runs it
is OQ-6, the owner's call. AC-32 and the web half of AC-19 belong to the web PR.

## 7. Consumer amendments (amend, never delete)

- `pac35_auth_flows_test.go`: Google fixtures gain `verified_email` (and a consumer or `hd`
  domain where the account must be domain-confirmed); the Microsoft runner gains an
  `/organization` answer and GUID ids; every redirect, cookie and kept-on-failure assertion is
  kept. `TestPAC35_CurrentUser_EveryDocumentedStatus`: `AddressStanding` added to the strict
  struct, strict decode kept, 500 sub-test added. The session-token seam takes `gen`, so the
  `failingTokenGenerator` helper changes signature only.
- `pac67_conferencing_test.go` `TestPAC67_MicrosoftCallbackKeepsRefreshAndFlipsOnlyTheCaller`:
  rewritten as a **connect** by alice (AC-12 shape) keeping every PAC-67 assertion: refresh token
  kept, loss ended, alice's status Outlook-connected, bob unaffected; and, since a connect never
  sets the work calendar (D-15), alice's provider is set by her own choice first, as the Settings
  control does.
- `pac21_4xx_bodies_test.go:193`, `pac67_world_test.go:224`: the Teams texts.
- `pac50_*`: break-glass fixtures seed a domain-confirmed binding.
- Granting-site tests (`handlers_teams_test.go`, `handlers_scheduling_links_test.go`,
  `handlers_manager_test.go`, `pac26_wire_keys_test.go`, `pac21_error_bodies_test.go`): a
  `confirmAddress` fixture helper gives the invitee domain-confirmed standing.
- `TestPAC50_SignInAuthorizeURLsCarryNoNewScope`: unchanged and must pass.
- `storage.AssignManagerTeamMembersMatchingFormalTeam`: removed (no caller at `ae5c2cf`;
  contract follow-up `deadCode…`), so no future caller can reintroduce address matching.

## 8. Operator command

`go run ./cmd/paceday-operator <command>` (reads `DATABASE_URL`), no HTTP surface:

- `free-address -account <uuid> -operator <name> -reason <text> [-squatter]`: address → a
  non-deliverable placeholder `freed+<uuid>@paceday.invalid`, standing unconfirmed, with
  `-squatter` every credential, connection and key removed; generation advanced; audited.
- `reassign -account <uuid> -identity google:<sub>|microsoft:<tid>:<oid> -evidence authoritative|mailbox
  -operator <name> -reason <text> [-keep-calendar-credentials] [-remove slack,notion,zoom,ai-keys]`:
  replaces (or writes) the binding, standing from the evidence, `provider_id = subject`, removes
  every calendar credential (Google, Microsoft, personal calendars) unless kept, generation
  advanced; audited with previous and new identity, evidence and what was removed.
- `advance-all -operator <name> -reason <text>`: every account's generation +1 (OQ-6); audited.

## 9. Gates for this PR

`go build ./...`, `go vet ./...`, `golangci-lint run`, the full backend suite through
`scripts/test-backend.sh` (database port 15499), `scripts/coverage-gate.sh` on its profile,
`scripts/openapi_migration_report.py --check`, the migration up/down/up test. Not run here:
the factory gate scripts (another agent holds the host checkouts), e2e (docker compose stack),
the web gate. Merge condition 2 (live provider answers) stays open.
