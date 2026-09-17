# Codebase notes — facts that were expensive to establish

Established by roughly twenty agent investigations on 2026-09-16/17, each reading
the source directly. Recorded so nobody re-derives them.

**Provenance caveat, which matters.** Everything here is source-reading, not
observed traffic: the session that produced it could not compile or run anything.
Treat each entry as a strong prior worth verifying cheaply, not as proof. Entries
confirmed independently by two agents say so. The moment you can run the code, the
cheapest thing you can do for the next reader is convert an entry here into a test.

---

## Live defects worth acting on before anything else

### No production path has ever created a per-user settings row
**Confirmed twice, independently.** `INSERT INTO settings` appears only at
`storage/settings.go:262,271`, two test fixtures and the e2e seed. Consequence:
focus-time scheduling errors (`no settings row for user <id>`, `focus_time.go:82-84`)
for every user except the earliest. Reachable over HTTP at `handlers_focus.go:37`
→ `eng.Run` → `focus_time.go:59-64`, not only via cron. On a deployment that had no
users when migration 018 ran, its `AND EXISTS (SELECT 1 FROM users)` guard means it
is broken for *everyone*. PAC-24.

### Settings is a global singleton behind per-user auth
`WHERE id = 1` throughout. `llmApiKey` is returned cleartext from that shared row.
`PUT` is a zero-value full replace, so one user saving wipes everyone's config.
Migration 018's own comment documents this as deliberate half-finished work.
`/api/auth/status` branches on the same global row, leaking another user's
`CalendarEmail`. `SaveMicrosoftToken` writes `microsoft_tokens` **and**
`calendar_provider='outlook'` at `WHERE id=1`, so one user connecting Outlook
shares their refresh token and flips the whole deployment (PAC-48).

### `ListUsersWithAutoSchedule` has no `user_id` predicate
The cron is dead today because of it — and it is a migration landmine: copying
`auto_schedule_enabled` on PAC-24's migration day would mass-write focus blocks
into every user's real calendar. PAC-49.

---

## Authentication and identity

- `requireAuth` accepts cookie `auth_token` **or** `Authorization: Bearer`, HS256
  only, claims `sub` (uuid) + `email`. **No server-side session store — a signed
  JWT *is* the session.** Nothing revokes an issued token: deleting an SSO
  provider, suspending, disconnecting all leave live 7-day sessions.
- **Cookie beats bearer.**
- `Secure` appears in **no** non-test Go file, including the session cookie.
  `SameSite` is set on `oauth_state`, `ms_oauth_state`, `sso_state` and
  `zoom_oauth_state`, but not on the Slack/Notion state cookies.
- **Org membership is self-service.** `AssociateUserWithOrg` (`storage/orgs.go:42-52`,
  called from `api/handlers_auth.go:76` — that is the load-bearing call site, not
  the SSO one) derives the org from the signup email domain and creates it if
  absent. "Has an `OrgID`" reduces to "signed up with a non-generic email domain".
- **There is no admin role anywhere.** Candidates checked and rejected: `users` has
  no role column (006); `organizations` has no owner (007); `team_members.role` is
  team-scoped and self-granted by whoever creates the team (013);
  `user_profiles.is_manager` is auto-detected from calendar shape (016). No env
  admin list exists.
- **`DomainMatchesOrg` is a suffix match; everything else is exact.** That single
  inconsistency is PAC-46/PAC-50. Exact: `GetSSOProviderByDomain`,
  `ListSSOProvidersByOrg`, `UpsertSSOProvider`, `DetectAuthProvider`,
  `AssociateUserWithOrg`, `EmailBelongsToDomain`.
- `onboarding_profile_selected` **does not exist on the backend**. The frontend's
  `RequireAuth` gates on it and synthesises it from `detected_at` or localStorage.
  `Team.tsx` likewise reads `is_manager` from localStorage, never from the server.

## The calendar layer

- **`googlecalendar.NewService` appears exactly once repo-wide**, at
  `backend/calendar/client.go:17`. 35 files import `calendar/v3` for types only;
  `mcp/` does not import `backend/calendar`. One constructor change opens all ten
  call sites — verified twice.
- **No endpoint override exists**: no `option.WithEndpoint`, no env var, no config
  above it, and the Google Go client honours no endpoint environment variable.
  This is why every calendar e2e journey is `test.fixme`. PAC-43.
- **`calendar.NewProvider` has zero production callers**, so `settings.calendar_provider`
  is ignored — but not *entirely*, and the exception is worse than the rule:
  `handlers_auth.go:105-110` reports `connected: true` for WebCal whenever a feed
  URL is stored, fetching nothing, and `freebusy_service.go:119-138` has a case
  only for `"outlook"`, so **WebCal free/busy is answered from Google**. The UI
  offers all three unconditionally. PAC-47.
- **The clock cannot be driven from outside the process.** `engine.SystemClock` is
  a package var `main.go` never replaces; `FreeBusyService.Clock` is exported and
  simply unassigned. One 15-minute cache is in scope for tests
  (`freebusy_service.go:140`); `webcal_client.go:26` is *not* a real cache, because
  `personal_reader.go:16` creates a fresh instance per call.
- `freebusy_service.go:121-137` **discards both errors** (`if err == nil`,
  `fetched, _ =`), so a connection refused still returns 200 with
  `coverage:"unknown"`. A test asserting "absence of error" here cannot fail.
- `backend/Dockerfile:10` already copies `ca-certificates.crt` into the `scratch`
  image — a TLS stand-in needs no extra trust store.
- Focus blocks **are Google Calendar events**, so `RunForUser` dies at `newCalOps`
  without a token. `ListFocusBlocksForWeek` and `FocusMinutesForDay` take **no user
  id** — focus blocks act as global busy time for every user's booking availability.

## The audit log

- **Seven action values, all literals**, and `WriteAuditLog` holds the only INSERT:
  `focus_created`, `focus_cleared`, `meeting_scheduled`, `meeting_moved`,
  `meeting_created`, `nlp_parsed`, `nlp_confirmed`. Any doc claiming dotted names
  such as `settings.update` is fiction — and that fiction reached
  `smart-calendar-flow/src/api/audit.test.ts:50`.
- `audit_log` has **no user column at all**, so `/api/audit` is not merely unscoped
  but unscopable without a migration. `details` carries meeting titles and raw NLP
  prompt text. PAC-23.
- All seven write sites **do** have a user in scope (the cron registers one entry
  per user; `RunForUser` refuses `uuid.Nil`). But `UserIDFromContext` returns the
  nil UUID rather than an error, so `CHECK (user_id IS NOT NULL)` would not catch
  it — only the foreign key would.
- **`POST /api/book/{slug}` is public**, reaches `booking.go:267 CreateEvent`, and
  records nothing. It is the genuine actor≠subject case.
- Auto-decline, personal blocker and daily recap write **no audit entries at all**.
- Four writers build `details` by unescaped string concatenation, so a meeting
  title containing `"` yields malformed JSON. The fourth is `handlers_nlp.go:67`.
- **`limit`**: `handlers_audit.go:14` discards `strconv.Atoi`'s error, so absent and
  non-numeric both arrive as `0`; `audit_log.go:16-18` is
  `if limit <= 0 || limit > 500 { limit = 100 }`. **Everything out of band becomes
  100.** Neither 50 nor 500 is reachable, despite both appearing in documentation.

## The LLM / NLP layer

- `POST /api/llm/test` **never reads `r.Body`** — it tests the stored config. A
  contract accepting a caller-supplied base URL would *create* an exfiltration
  channel: `llm_factory.go:29` pairs the stored key with the stored base and
  `parser.go:31,43` sends `Authorization: Bearer <key>` to it.
- On `azure_openai`, `bedrock` and `vertex` the credential is **ambient, not
  submitted** — `llm_azure_openai.go:28-50` mints a token via
  `azidentity.NewDefaultAzureCredential` and sends it to `c.Endpoint` verbatim.
  That is the deployment's own cloud identity, worth more than the LLM key. A
  request-shape fix covering only OpenAI and Anthropic is insufficient.
- **No test anywhere asserts that a prompt produces a correct parse.** Every test in
  `backend/nlp` is a plumbing test against canned `httptest` fixtures: which client
  the factory returns, HTTP error handling, prompt *data* assembly. The 165-line
  timezone-aware scheduling prompt is unverified against any real model. This is
  the gap `make evals` is meant to close.

## Wire-shape systemics

From the 92-finding audit in `api-audit.md`. These are the recurring ones:

- **Structs with no json tags leak PascalCase field names** onto the wire:
  `storage.FocusBlock`, `engine.FocusBlock`, `calendar.TimeSlot`,
  `calendar.GenericEvent`, `storage.SSOProvider`, the manager structs. One
  `POST /api/freebusy` response contains *both* casings for the same data.
- **Two error encodings coexist**: `writeError` (JSON envelope) and `http.Error`
  (text/plain), sometimes mixed within one file — 111 `http.Error` call sites. The
  frontend parses JSON, so those surface message-less. `http.Error` also overwrites
  a Content-Type that was just set, which is why the 401 body is JSON served as
  `text/plain`.
- **Backend Settings is camelCase, frontend snake_case.** Counts independently
  confirmed: 50 backend keys, 34 frontend, **4 bridged**, 1 case-invariant
  (`timezone`), 26 frontend properties differing from a real backend counterpart
  only by case and therefore reading `undefined` every time.
- Handlers returning the raw Google `calendar/v3.Event` where the frontend expects
  the `CalendarEvent` DTO: `PATCH /api/events/{id}`, `POST /api/schedule/create`,
  `POST /api/nlp/confirm`.
- Nil slices marshal to `null`, not `[]`, throughout — and the frontend maps over them.
- Silent-success writes that never check `RowsAffected`: `RespondToHostInvite`,
  `leaveSchedulingLink`, `RemoveLinkHost`, `DeleteSSOProvider`.
- **31 operations have no consumer anywhere** in the frontend — the whole of habits,
  analytics, meeting briefs and the Slack/Notion integrations.

## Frontend

- **Zero `data-testid` attributes** anywhere in `src`. Required names are fixed in
  `e2e/TESTIDS-REQUIRED.md` and are a contract with the e2e suite. PAC-44.
- `src/contracts/managerTeam.ts` is the good pattern: strict zod that rejects
  unknown fields rather than normalising protocol drift away. Keep that strictness.
- `src/api/client.ts` is a ~1,400-line hand-written client behind the `ApiPort`
  interface, which is the single mock seam. Do not replace it with a generated
  client — that is why `typed-openapi` (standalone schemas) was chosen over
  `openapi-zod-client` (which emits a competing zodios client).

## Tooling gotchas

- **`oapi-codegen` cannot read OpenAPI 3.1**, and the bundle is 3.1
  (`type: [x,'null']`, `const`, schema-level `examples`).
  `scripts/openapi_downconvert.py` exists as a workaround and has never been fed to
  a generator.
- **`.golangci.yml` is v1 schema**; golangci-lint v2 rejects it outright. Downgrade
  or run `golangci-lint migrate`. The pre-commit hook hits this too.
- `backend/go.mod` needs `github.com/oapi-codegen/runtime` before any generated Go
  compiles.
- The bundle hash moves whenever *any* fragment changes, so a `contractHash` in a
  feature manifest goes stale as soon as a concurrent feature lands. A
  `contractFragmentHash` over the owning fragment is the stabler anchor.
- `scripts/openapi_assemble.py` is pure Python + PyYAML. `--check` is the drift gate.
- Concurrent agents editing *different* fragments is safe; both regenerating the
  bundle is last-writer-wins, so regenerate once at the end. Agents writing to a
  shared scratchpad root **did** overwrite each other — give parallel agents
  distinct output paths.
