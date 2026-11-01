# Spec — PAC-84: Encrypt stored provider credentials and API keys at rest

- **Linear**: https://linear.app/paceday/issue/PAC-84/encrypt-stored-provider-credentials-and-api-keys-at-rest
- **Status**: challenged 2026-11-01 (accept-with-changes, applied below;
  owner Q-1..Q-3 answered 2026-11-01)
- **Author**: spec-author; challenge edits by spec-challenger (see "Challenge
  log" at the end)
- **Base**: `origin/main` at `953f96e`. Citations are to that commit unless a
  branch is named.
- **Inherited scope**:
  - PAC-81 finding **F-D5** (`docs/specs/NEW-deploy-hardening.md:366`, §10
    follow-up at `:1181-1182`): dumps hold provider credentials in plain text.
    **Resolved by this spec.**
  - `docs/security.md:90` (open risk 4: "LLM provider API keys + SMTP creds
    in plaintext `settings` columns") and `docs/security.md:102` (open risk
    10, PAC-81 F-D5: backups contain users' provider credentials); the asset
    rows at `:15` and `:78` say the same. **Resolved** for every column in
    §2.1, and all four places are updated in the same change; SMTP
    credentials named there are environment variables, not stored rows
    (`.env.example:228`), so nothing to encrypt.
  - Deferrals recorded by earlier specs, which this ticket now owns:
    PAC-22 (`docs/specs/PAC-22.md:850-852`), PAC-24 (`PAC-24.md:612-614`),
    PAC-66 (`PAC-66.md:483`), PAC-67 (`PAC-67.md:783`).
  - API audit: no open finding concerns storage at rest; API-001
    (`docs/factory/api-audit.md:259`) is the disclosure of the SSO secret over
    HTTP, already fixed by PAC-22 and must stay fixed (§6).
  - `x-uncertain`: none — no HTTP operation changes shape.

## 1. Problem

Paceday stores, in ordinary database columns, the secrets that let it act for
its users elsewhere: Google, Microsoft and Zoom OAuth access and refresh
tokens, Slack and Notion tokens, users' own AI provider keys, the
organisation's AI key (PAC-73) and organisations' SSO client secrets. They are
stored exactly as received.

So anything that can read the database — a nightly backup copied off the host,
a dump handed to someone for debugging, a stolen disk, a read-only SQL
injection, a misconfigured replica — can act as every user at every connected
provider: read and write their calendars, read their Slack and Notion
workspaces, spend their AI budget, and impersonate an organisation's identity
provider client. The refresh tokens stay valid for months, so a backup leaked
long after it was taken is still a live credential set.

PAC-81 accepted this for the time being by telling operators to treat every
dump as a secret (`docs/runbook.md:247-262`). That is a procedure, not a
protection: the backup script even copies `.env` beside every dump
(`scripts/ops/backup.sh:87`). This ticket removes the exposure itself: a
database copy on its own must reveal no usable credential.

Who is affected: every user with a connected account (exposure), the operator
(who today must guard every dump as if it were the credentials themselves),
and organisation admins (SSO secret, org AI key).

## 2. Current behaviour

### 2.1 Inventory of stored secrets

Every column below holds a secret today, in plain text. "R" = read paths,
"W" = write paths. Hot paths are marked **hot**.

| # | Secret | Defined | Written by | Read by |
|---|---|---|---|---|
| S1 | Google OAuth access + refresh token (one row per user and calendar) | `oauth_tokens.access_token`, `.refresh_token` — `001_initial.up.sql:28-29`; per-user since `017_oauth_tokens_per_user.up.sql:11-12` | connect `auth/token_store.go:25-33` (from `api/handlers_auth.go:77`); renewal write-back `token_store.go:46-49`; delete `:79` | `auth.LoadUserToken` `token_store.go:65-70`, called from **hot** public booking (`api/handlers_booking.go:103`, unauthenticated), free/busy `engine/freebusy_service.go:157`, calendar ops `engine/cal_iface.go:33`, `engine/booking.go:322`, `engine/habits.go:250`, `engine/auto_decline.go:77` (cron), `api/handlers_auth.go:156`, `api/handlers_conferencing.go:182` |
| S2 | Microsoft (Outlook) OAuth token record — JSON of access, refresh, expiry | `settings.microsoft_tokens` — `003_calendar_providers.up.sql:2` (nullable: NULL and `''` both mean "none"; PAC-71's operator removal writes NULL) | connect `auth/microsoft_oauth.go:70-93`; renewal `storage/credentials.go:171-173` via `auth/credential_lifecycle.go:224-228`; remove `credentials.go:190-195` | `microsoft_oauth.go:113-127`; **every settings read** `storage/settings.go:255` (§2.2); `calendar/client_factory.go:17-20` |
| S3 | Zoom OAuth token record — JSON | `settings.zoom_tokens` — `005_conferencing.up.sql:2` | connect `credentials.go:123-150` (from `api/handlers_conferencing.go:149-154`); renewal `credentials.go:160-166`; remove `:180-183` | `credentials.go:108-118`; **every settings read** `settings.go:256`; `conference/factory.go:22-29`; `api/handlers_conferencing.go:51-54,186-187` |
| S4 | Personal calendar OAuth token (JSON `oauth2.Token`) | `personal_calendars.credentials_json` — `004_personal_calendars.up.sql:6` | **no API writer**: create (`api/handlers_personal.go:68-90`) never sets it; only legacy rows and the e2e seed (`e2e/seed/seed.sql:129-131`) | `storage/personal_calendars.go:32,49,71,166`; `calendar/personal_reader.go:19-26` (personal-blocker cron, every 30 min, `main.go:170-175`) |
| S5 | Personal calendar URL — for provider `webcal` a private iCal address, i.e. a bearer credential to that calendar | `personal_calendars.url` — `004_personal_calendars.up.sql:5` | `handlers_personal.go:86-90`; `personal_calendars.go:57-71` | `personal_reader.go:15-17`; returned to its owner (`handlers_personal.go:45`) |
| S6 | Work calendar webcal URL (same nature as S5) | `settings.webcal_url` — `003_calendar_providers.up.sql:3` | `settings.go:341,389` (every settings save) | **every settings read** `settings.go:255`; `calendar/client_factory.go:23-26`; `handlers_auth.go:151` (presence only) |
| S7 | Slack user token / Notion token; Slack bot token | `workspace_connections.access_token`, `.bot_token` — `014_workspace_connections.up.sql:5-6` | `storage/integrations.go:23-39` (from `api/handlers_integrations.go:82,220`) | `integrations.go:42-56`, from daily recap (`engine/daily_recap.go:79,106`, cron), meeting briefs (`engine/meeting_brief_service.go:86-93`), `api/handlers_daily_recap.go:159,175`, `handlers_integrations.go:99,237` |
| S8 | Users' AI provider keys: one per provider plus the unbound legacy key | `settings.llm_api_key` (`001_initial.up.sql:21`), `llm_api_key_openai/_anthropic/_azure_openai/_bedrock/_vertex` (`029_llm_bring_your_own.up.sql:16-20`) | `settings.go:323-410` (every settings save writes all six) | **every settings read** `settings.go:248-250`; AI calls via `nlp/ai_source.go:28`; key redaction `api/handlers_llm.go:80` |
| S9 | Organisation AI key (PAC-73 part 2) | `org_ai_configurations.api_key` — `031_llm_org_ai.up.sql:27` | `storage/org_ai.go:410-420` (`WriteOrgAIConfig`) | `org_ai.go:98` (column list for `ReadOrgAIState` `:135`, `GetOrgAIConfig` `:168`) — read on every member AI call |
| S10 | OIDC client secret (PAC-22/PAC-50) | `sso_providers.oidc_client_secret` — `008_sso_providers.up.sql:9` | `storage/sso_providers.go:81-93, 222, 239-247` | `sso_providers.go:30,274`; SSO sign-in `api/handlers_sso.go:73,133` |
| S11 | SAML signing certificate | `sso_providers.saml_cert` — `008_sso_providers.up.sql:12` | none: SAML writes are refused (`handlers_sso.go:344-345`); legacy rows only | `sso_providers.go:30,274`; presence flag `handlers_sso.go:387` |

Examined and **not** in the inventory:

- **Team invitation tokens** (`team_invites.token`, `013_teams.up.sql:22`) are
  looked up by value (`storage/teams.go:244`), so they cannot be stored with
  randomised encryption; they need a one-way hash. Proposed as a follow-up
  (§5). Accepting the follow-up is safe because a token from a dump is not
  usable on its own: accepting requires being signed in as the invitee's
  address (`api/handlers_teams.go:313-317`) and invites expire after 7 days
  (`storage/teams.go:232`). The dump test (AC-1) does not cover them.
- **Sessions** are stateless HS256 cookies signed with `JWT_SECRET`
  (`main.go:248-250`); no session secret is stored. PAC-71
  (`origin/impl/pac-71-identity-binding-backend`, #242, unmerged) adds a
  per-account session *generation* number, identity bindings and an operator
  audit table (`033_identity_binding.up.sql` on that branch) — identifiers,
  not secrets.
- **Step-up grants** (`030_sso_admin_stepup.up.sql:19-29`) hold no secret;
  OAuth state lives in a cookie.
- **Free-text JSON** (`audit_log.details`, `sso_events.details`) is written
  without token material by design (`auth/credential_lifecycle.go:59-61`;
  PAC-73 redaction). Not encrypted, but covered by the dump test (AC-1),
  which would catch a regression.
- **Client secrets of Paceday's own OAuth apps** (Google, Microsoft, Zoom,
  Slack, Notion) are environment variables, not rows (`.env.example:103-215`).
- **AI destinations** (`settings.llm_base_url`, `azure_endpoint`,
  `org_ai_configurations.base_url`, `azure_endpoint`): addresses, not
  credentials. A destination carrying userinfo or a query string (the only
  ways to smuggle a key into a URL) is refused before use
  (`nlp/user_destination.go:161,223-224`, `nlp/destination.go:129`). Not
  encrypted; covered by the dump test only through the canaries above.
- **Cached provider content** (`meeting_briefs.slack_results`,
  `.notion_results`, `014_workspace_connections.up.sql:20-21`) holds search
  results, not credentials. The free/busy cache is in memory
  (`engine/freebusy_service.go:56`). No other table or column whose name or
  content suggests a token, secret, key, password, certificate, signing
  secret or private URL exists in migrations `001`–`032` (checked by grep of
  every `CREATE TABLE`/`ADD COLUMN`), nor in PAC-71's `033`. The MCP server
  has no database access.

### 2.2 How the secrets travel today

- One settings read loads S2, S3, S6 and all six S8 keys whether or not the
  caller needs them: `settingsColumns` (`settings.go:240-261`) is used by
  `GetOrCreateSettingsByUser` (`:266-307`), which has 22 non-test call sites
  across `api/`, `engine/` and `nlp/` (request handlers and every cron).
  The settings save (`settings.go:323-410`) reads the row and writes all six
  S8 keys back, so a settings `PATCH` depends on reading every key.
- **Read failures are mapped differently per path today**, and decryption
  failure will land on whichever mapping the path already has:
  - *status / presence* paths turn **any** read error into "not connected":
    `handlers_auth.go:141-142` (Outlook) and `:156-158` (Google),
    `handlers_integrations.go:99-101` (Slack) and `:237` (Notion),
    `handlers_conferencing.go:182-188` (usable providers, errors discarded);
  - *best-effort* paths skip silently: meeting briefs
    (`engine/meeting_brief_service.go:86-93`), free/busy (degrades to
    "unknown", `engine/freebusy_service.go:145-163`), public-booking coverage
    (`handlers_booking.go:103`);
  - *host availability* (`engine/booking.go:228-231`) turns any credential
    error into "unreadable → busy" (PAC-72 D-6), while a *storage* read
    failure fails the listing (PAC-72 D-7, `engine/booking.go:156-157`);
  - *lists* fail whole: one bad row in `ListPersonalCalendarsByUser`
    (`personal_calendars.go:162-170`) fails the user's list endpoint
    (`handlers_personal.go:55-58`) and that user's personal-blocker run
    (`engine/personal_blocker.go:53`).
- **Disconnect reads the stored value and acts on it after removal**:
  `removeCredential` (`storage/credentials.go:197-222`) returns the raw stored
  value, and the Zoom disconnect decodes it to revoke the grant at Zoom
  (`api/handlers_credentials.go:125-147`, `revokeZoom`). Removal already
  succeeds "whether or not it decodes".
- `LoadUserToken` picks the row with the latest `oauth_tokens.updated_at`
  (`token_store.go:65-70`); any write that touches `updated_at` changes which
  token is used when a user has more than one row.
- At start, the crons are scheduled (`main.go:168-176` and before) **before**
  the `JWT_SECRET` check (`main.go:248-250`); "refuses like `JWT_SECRET`" is
  therefore not by itself "refuses before any background work".
- SSO provider rows are upserted by `lower(domain)` and an update rewrites
  `domain = lower(domain)` in place (`sso_providers.go:217`): the stored
  domain text of a row can change; its `id` and `lower(domain)` cannot.
- `personal_calendars.user_id` is nullable (`018_…up.sql:23-29` only
  backfilled it); rows owned by nobody may exist on old deployments.
- Presence is decided by comparing the stored value with the empty string, in
  Go (`handlers_conferencing.go:51`, `client_factory.go:17,23`,
  `handlers_sso.go:384,387`, `settings_ai.go:113`) and in SQL
  (`credentials.go:165,172`: "update only while still connected";
  `token_store.go:30`: an empty refresh token keeps the stored one).
- A renewal refused with `invalid_grant` marks the credential **lost**
  (`auth/credential_lifecycle.go:54-72`, PAC-67); any other failure is
  temporary.
- PAC-72 D-6 (owner, 2026-11-01): a host whose calendar cannot be read is
  **busy** for public booking.
- Migrations are SQL only, run at start, up only (`storage/db.go:37-60`,
  `.env.example:38`). There is no Go data-migration step today.
- The operator CLI `paceday-operator` exists only on the PAC-71 branch
  (`backend/cmd/paceday-operator/main.go`, subcommands `free-address`,
  `reassign`, `advance-all`, with an `operator_audit` table whose `action`
  CHECK allows only those three, `033_identity_binding.up.sql` on that branch).
- No encryption code exists in the backend (confirmed by PAC-81,
  `NEW-deploy-hardening.md:366`).
- Backups: `scripts/ops/backup.sh:87` copies the whole `.env` into every
  backup set; the runbook says a dump holds credentials in plain text and that
  the dump and `.env` must be kept together (`docs/runbook.md:249-262`).
  The restore rehearsal checks a seeded Google token survives by comparing the
  raw column (`scripts/ops/rehearse-backup-restore.sh:164-166,212`).
- Tests that read or write these columns raw: the e2e seed
  (`e2e/seed/seed.sql:109-131`), `e2e/tests/llm-bring-your-own.spec.ts:313-330`
  (writes and reads `llm_api_key` raw), the rehearsal script above, and Go
  tests that insert or compare these columns by SQL (23 `_test.go` files under
  `backend/` name one of the columns).
- Every place that boots a backend process and would now need a key:
  `docker-compose.yml:21-27` (dev, `JWT_SECRET=${JWT_SECRET:-}`),
  `docker-compose.prod.yml:67-79`, `e2e/docker-compose.e2e.yml:93`,
  `e2e/tests/startup.spec.ts:36-40` (`BOOTABLE`, the base of every refusal
  case, which asserts only "exits non-zero, says refusing to start" — a
  missing key would make every refusal case pass for the wrong reason) and
  `:72-90` (boots with `docker-compose.prod.yml`'s backend `environment:`
  block, read by `e2e/lib/docker.ts` `productionReferenceEnvironment`),
  `scripts/ops/rehearse-backup-restore.sh:132`, and the Go suite
  (`scripts/test-backend.sh:48-53`).

## 3. Desired behaviour

- Every secret in the inventory is stored encrypted. A copy of the database —
  a dump, a backup, a replica, a disk image — reveals none of them, and
  holding that copy is not enough to decrypt them: the key lives outside the
  database and outside the backup set.
- Users notice nothing. Connected accounts keep working through the upgrade
  with no reconnect; the API returns exactly what it returns today (write-only
  keys stay write-only).
- A stored secret is bound to its place: copying an encrypted value into
  another user's row, another organisation's row or another column does not
  make it usable there — it fails to decrypt.
- The operator supplies one key. The backend refuses to start without it or
  with a key that cannot open the stored data, and says which and why without
  ever printing key material. A key can be rotated without downtime beyond a
  restart and without touching every row.
- Existing deployments are converted in place, online, after the upgrade;
  the conversion can be interrupted and resumed, and running it twice changes
  nothing. Until it finishes, old plain values keep working.
- The operator can see the state at any time (how many values are still
  plain, which keys are in use), convert explicitly, rotate, prepare a
  downgrade, and — if the key is lost — reset the stored secrets so the
  deployment can start again, with users reconnecting.
- Nothing in logs, error reports or responses ever contains a secret or the
  key, including when decryption fails.
- The runbook and the environment reference say where the key lives, how to
  back it up separately from the dumps, how to rotate it, how to restore, and
  what losing it costs.

## 4. Acceptance criteria

Terms: "**the inventory**" is §2.1 S1–S11. A "**canary**" is a distinct random
string of at least 24 characters written as a secret value. "**Encrypted
deployment**" is one started with a valid key after the conversion finished.
Decisions D-n are in §9. Test levels: `[unit]` Go test with a real Postgres
(`internal/testdb`), `[e2e]` docker compose stack, `[ops]` script test in the
style of the PAC-81 Part D tests.

### Confidentiality — the dump test

AC-1. Given an encrypted deployment where a canary has been stored in every
inventory secret through its normal write path (S4 and S11, which have no
write path, by the legacy seed then the conversion), when the database is
dumped with `pg_dump` in plain SQL format, then the dump contains none of the
canaries — searched as-is, base64-encoded, hex-encoded and as any 12-character
substring — and does not contain the key, in any of those encodings. [e2e]

AC-2. Given the dump from AC-1 restored into an empty database, when the
backend starts with the same key, then every canary reads back exactly through
its normal read path; and when it starts with no key or with a different key,
it refuses to start (AC-8, AC-9). [e2e]

AC-3. Given any inventory secret written twice with the same value, then the
two stored values differ, and neither equals or contains the value. [unit]

AC-4. Given a backup taken with `scripts/ops/backup.sh` on an encrypted
deployment configured as the runbook says, then no file of the backup set
contains the active key or any previous key (the env file copy does not hold
them); and the backup script refuses — exit non-zero, naming the runbook
section, writing no file (no partial dump left behind either) — when the env
file it is told to copy contains `PACEDAY_ENCRYPTION_KEY` or
`PACEDAY_ENCRYPTION_KEY_PREVIOUS` with a non-empty value. A commented-out or
empty line does not trigger the refusal. [ops]

### Integrity — values are bound to their place

AC-5. Given users A and B each with a stored secret in the same inventory
column, when A's stored value is copied by SQL into B's row, then reading B's
secret fails with a decryption error, A's plaintext is never returned or used,
and B's request behaves as AC-12 says. The same holds for a copy between two
organisations (S9, S10) and between two columns of the same row (e.g. one AI
provider's key into another provider's slot, the access-token column into the
refresh-token column). [unit]

AC-6. Given a stored encrypted value with any single byte changed, when it is
read, then it fails with a decryption error (never returns altered
plaintext). [unit]

AC-7. Given an inventory secret whose value is the empty string (not
connected) — or NULL, for the nullable S2 — then it is stored as the empty
string (NULL stays NULL), and every presence check
listed in §2.2 gives the same answer as today: the existing tests that pin
"not connected", `has_*` flags, "renewal does not re-store after disconnect"
(PAC-67) and "empty refresh token keeps the stored one" pass unchanged. [unit]

### Key handling at start

AC-8. Given no key configured, when the backend starts, then it exits
non-zero before listening, before scheduling any cron and before any
conversion work, with a message naming the key variable and the runbook
section — on a fresh database as well as on one holding encrypted data
(D-5). The message contains "refusing to start" (the marker
`e2e/tests/startup.spec.ts:68` checks). [e2e]

AC-8b. Given every existing refusal case of `e2e/tests/startup.spec.ts`,
then its base environment carries a valid test key, so each case is still
refused for its own reason: each case's output names its own variable and
does not name the encryption key variable. The positive boot case (`:72-90`,
production's own backend environment) still boots, which requires
`docker-compose.prod.yml` to hand the key to the backend in a way that test
reads (or the test to be updated to add it, stating why). [e2e]

AC-9. Given a key that does not open the stored data keys (wrong key), or a
malformed key (not decoding to exactly 32 bytes), when the backend starts,
then it exits non-zero before listening; the message names the problem and
the key identifiers involved, and contains no key material in any encoding.
[unit + e2e]

AC-10. Given stored data keys wrapped by a key identifier that is neither the
active nor a configured previous key, when the backend starts, then it refuses,
naming that identifier. [unit]

### Rotation

AC-11. Given an encrypted deployment on key K1, when the operator configures
K2 as active and K1 as previous and restarts, then the backend starts, every
data key is re-wrapped under K2 within that start (D-6), every secret still
reads back, the status command (AC-20) reports zero data keys under K1, and a
further restart with K2 alone (K1 removed) starts and reads every secret. No
inventory row is rewritten by this step (row count of changed values = 0).
[unit + e2e]

AC-11b. Given the operator runs the data-key rotation command, then new
writes use the new data key, the command re-encrypts existing values in
batches (resumable as AC-14), and afterwards the status command reports zero
values under the old data key, which can then be retired. [unit]

### Conversion of existing plain values

AC-12. Given a database at the migration version before this work with a
plain canary in every inventory secret (the pre-upgrade state), when the new
backend starts with a key, then it serves requests at once; every secret
reads back correctly during the conversion (old plain values keep working);
and within one conversion run the status command reports zero plain values
in every inventory column, with no user reconnecting and no value changed:
every secret reads back the same plaintext, and **no other column of any
converted row changes** (in particular `oauth_tokens.updated_at`, which
decides which token `LoadUserToken` uses, `settings.updated_at`,
`workspace_connections.connected_at`, `sso_providers.updated_at`,
`org_ai_configurations.configuration_version` and `.updated_at`). [e2e for
the flow; the "no other column" half [unit]]

AC-13. Given the conversion is running, when a user writes a new value to a
row the conversion has read but not yet updated, then the user's new value is
what is stored afterwards (the conversion never overwrites a newer write).
[unit]

AC-14. Given the conversion is interrupted (process killed) part-way, when the
backend starts again, then the conversion completes; no value is encrypted
twice (every secret still reads back its original plaintext); and a second
complete run changes zero rows. [unit]

AC-15. Given an encrypted deployment, when a plain value appears in an
inventory column (e.g. written by SQL), then it still reads (D-7), the
status command counts it as plain, and the next conversion run encrypts it.
[unit]

### Failure while serving

AC-16. Given one user's stored secret fails to decrypt (AC-5/AC-6 state),
when that user's request needs **that secret to act** (calendar read or
write, conferencing link, Slack recap send, AI call, SSO sign-in), then the
response is the generic JSON server-error envelope (no detail), the server
log records the table, column, row identity, data-key id and the user id —
and no value; and the credential is **not** deleted, **not** marked lost (no
credential-loss record, no `connection_lost` activity entry), and no call is
made to the provider with it. In every case below (AC-16b..AC-18) the same
log line is written and the same "not lost, never sent" holds. [unit]

AC-16b. Given the same state, the **status and presence** paths listed in
§2.2 (Google/Outlook/webcal calendar status, Slack and Notion status,
usable conferencing providers, `has_*`/`*Set` flags) answer exactly as they
answer today for a stored value that cannot be read: the calendar, Slack,
Notion and conferencing status report it as not connected / not usable (the
existing mapping), while the write-only presence flags (`llmApiKeySet`,
`hasOidcClientSecret`, …) still report it as **set**, because presence is the
stored value being non-empty (D-3) and needs no decryption. [unit]

AC-16c. **A user or admin can always get out of the state on their own**:
given one stored secret that fails to decrypt, then (a) reading and saving
the user's settings succeeds and every other field and secret of the row is
unaffected (the failing secret is neither returned nor overwritten by a save
that does not change it); (b) reconnecting the provider, re-entering the AI
key or SSO secret, or entering an empty value replaces it, after which it
reads normally; (c) disconnecting the provider removes it (status 200) and
makes no provider call with it (the Zoom revocation reports `unconfirmed`,
as today for an undecodable value); (d) one undecryptable personal calendar
does not hide or fail the user's other personal calendars in the list
endpoint, and can be deleted. [unit]

AC-17. Given a background job (auto-schedule, auto-decline, personal-blocker,
daily recap, meeting briefs) iterating users or items, when one secret fails
to decrypt, then only the work needing that secret is skipped (that user, or
for the personal-blocker that one calendar) with the AC-16 log line, and
every other user and item is processed. [unit]

AC-18. Given a public booking link whose host's stored calendar credential
fails to decrypt, then that host counts as unreadable — busy, slots refused —
exactly as PAC-72 D-6 treats an unreadable calendar, never as free; a
decryption failure is **not** a PAC-72 D-7 storage failure (it must not fail
the whole listing with a 500). The free/busy service degrades to "unknown"
as for any credential it cannot use. [unit]

### No secret in logs, reports or responses

AC-19. Given a test run of AC-1, AC-5, AC-6, AC-9, AC-12, AC-16 and the
operator commands with the log output and the error-reporter transport
captured, then neither capture contains any canary or the key, in the
encodings of AC-1, nor any stored ciphertext. [unit]

AC-19c. The conversion and the data-key rotation never send a plain secret
to the database server as a statement parameter or literal (Postgres may log
parameters of slow or failing statements, and those logs outlive the
conversion): given statement logging captured during a conversion run, it
contains no canary. Writing plaintext back is allowed only in
`decrypt-all`, which exists to do exactly that. [unit]

AC-19b. No API response, for any caller, contains a stored write-only secret
(PAC-24, PAC-22, PAC-73): the existing tests that pin this pass unchanged;
S5 and S6, which their owner can read back today, are returned to their owner
unchanged in value. [unit]

### Operator command

AC-20. Given the operator command's encryption status subcommand, when run
against any deployment, then it prints, per inventory column, the number of
empty, plain and encrypted values and the encrypted counts per data key, the
data keys with the key identifier wrapping each, and exits 0; it prints no
value and no key. With `--check` it exits non-zero while any plain value
remains (for scripting the runbook). [unit]

AC-21. Given the conversion, data-key rotation, prepare-downgrade (decrypt
all) and lost-key reset subcommands (D-8), then each refuses without
`--operator` and `--reason`, records who, why, what and how many in the
operator audit record, and is safe to re-run (a second run reports zero
changes). [unit]

AC-21b. Given two of {the backend's automatic conversion, `convert`,
`rotate-data-key`, `decrypt-all`, `reset-lost-key`} started at the same time
(two processes), then at most one runs; the other waits or exits non-zero
saying which one holds the lock, and no value ends up under a data key that
is not recorded, nor double-encrypted. [unit]

AC-21c. Given `decrypt-all` or `reset-lost-key` is run while a backend process
is connected to the database, then it refuses (non-zero, naming the runbook
step "stop the backend first") and changes nothing — a running backend
would re-encrypt values behind `decrypt-all`, and would keep serving with
data keys `reset-lost-key` removed. [unit]

AC-21d. The existing PAC-71 subcommands (`free-address`, `reassign`,
`advance-all`) and `encryption status` work without any key configured;
`convert` and `rotate-data-key` need the active key (and refuse like AC-9
without it); `reset-lost-key` needs only the new key it will be started
with. [unit]

### Downgrade, rollback and lost key

AC-22. Given an encrypted deployment, when the schema down step of this work
runs while any encrypted value or data key exists, then it fails without
changing anything, with a message pointing to the prepare-downgrade
subcommand; after that subcommand has written every value back in plain, the
down step succeeds, the schema equals the schema before this work's up step
(except as §7 says for `operator_audit`), and every inventory column holds
its original plaintext — so the previous release's read paths, which read
the columns raw, read every secret. The runbook names the exact command that
runs the down step (the backend only ever migrates up, `storage/db.go:59`).
[unit]

AC-23. Given the key is lost, when the operator runs the lost-key reset with a
new key configured, then every inventory secret ends in the state the
**owner's own removal** leaves it in today, without any provider call:
Google (S1) and Slack/Notion (S7) rows are deleted (an emptied row would
still read as connected: `handlers_auth.go:156`, `handlers_integrations.go:99`);
Outlook (S2) and Zoom (S3) as `RemoveMicrosoftCredential` /
`RemoveZoomCredential` leave them (conferencing back to Meet where it was
Teams/Zoom); every recorded credential loss for a removed credential is
cleared; personal calendars (S4/S5) holding an encrypted value are disabled
with their secret emptied; the webcal URL (S6), the AI keys (S8, S9) and the
SSO client secret and certificate (S10, S11) are emptied. Every data key is
removed; the backend then starts with the new key; each affected user sees
those connections as not connected (not "lost"); organisations see no org AI
key and no SSO client secret (SSO sign-in for that domain fails until an
admin re-enters it; break-glass still works); the audit record lists the
counts per secret. Plain values (D-7) are left untouched, and nothing outside
those rows' secret columns and the columns named here changes. [unit]

### Environment reference, runbook, performance

AC-24. Given `.env.example`, then it documents the active key and the
previous-keys variables in the PAC-81 `#:` line format (status, default,
purpose) without a value line that copying `.env.example` to `.env` would
fill in (the key belongs in the separate file of D-9, never in the `.env` the
backup copies), its SECRETS paragraph says the key is kept apart from the
backup set, and `backend/config/env_reference_test.go` passes; the e2e stack
sets a test-only key. [unit]

AC-25. Given `docs/runbook.md`, then it has a section on the encryption key
(generate, where to keep it, back it up separately, rotate, retire a previous
key only after the backup retention has passed, restore, what losing it
costs, the lost-key reset), the "Backups are secrets" text no longer says
dumps hold credentials in plain text but says what a dump from before the
conversion still holds, and the restore rehearsal
(`scripts/ops/rehearse-backup-restore.sh`) supplies the key separately from
the backup set and proves a seeded credential reads back through the backend
after restore — not by comparing raw columns — and adds a negative control:
the restored backend started with another key refuses (AC-9). [ops]

AC-26. Given the backend at start, then the key-encryption key is used only to
unwrap data keys at start (the number of unwrap operations equals the number
of stored data keys; zero per request, counted over a test that serves
requests and runs one cron tick), and a Go benchmark of decrypting every
secret of one settings row with all nine filled (no database I/O) reports
under 1 ms per operation on the CI runner. [unit]

AC-27. Given a fresh database (no data key) and two processes starting at
the same time with the same key (backend and `paceday-operator`, or two
backends), then exactly one active data key exists afterwards and every value
either wrote is readable by both. [unit]

AC-28. `make verify` (lint, unit, coverage, e2e) passes on a checkout with no
developer-supplied key: every test path that boots the backend or writes an
inventory secret (§2.2 last bullet) gets a committed, clearly test-only key.
`docker-compose.yml` (dev) refuses without a key exactly like production, and
`.env.example` and the README say how to generate one for development. [e2e]

## 5. Explicitly out of scope

- **A key-management service** (cloud KMS, Vault). The key comes from the
  environment; the design keeps one place where the key-encryption key is
  used so a KMS can replace it later (D-1). Owner Q-1 answered 2026-11-01:
  environment now, KMS later (follow-up, §10).
- **Team invitation tokens**: they are looked up by value and need hashing,
  not encryption. Follow-up proposed: "Store team invitation tokens hashed".
- **Plain values in pre-upgrade backups and in Postgres' own files.** Backups
  taken before the conversion keep plain values until they age out (14 days,
  owner Q-O3); dead row versions and WAL may hold old values until Postgres
  reuses the space. The runbook says so; revoking tokens held in old backups
  is not attempted. `pg_dump` reads only live rows, so AC-1 is unaffected.
- **Encrypting non-secret personal data** (booker names and emails, calendar
  event text, meeting briefs, AI configuration other than keys).
- **Paceday's own OAuth client secrets, `JWT_SECRET`, `SMTP_PASS`,
  `MCP_AUTH_TOKEN`**: environment, not rows.
- **Per-tenant keys** (one key per organisation). Not needed for the problem;
  could follow a KMS.
- **Changing any HTTP operation.** No contract change is expected; if the
  contract stage finds one (e.g. the 500 envelope of AC-16 is already the
  documented one), it is a documentation change only.

## 6. What must not change

- **Write-only secrets stay write-only** (PAC-24, PAC-22 D-*, PAC-73): no API
  returns a stored key, token or SSO secret. Protected today by
  `TestPAC22_ListNeverReturnsEitherSecret`,
  `TestPAC22_CreateResponseNeverReturnsTheSecret` and the PAC-24/PAC-73 tests
  in `backend/api`.
- **PAC-67 credential lifecycle**: renewal write-back only while connected,
  empty refresh token keeps the stored one, `invalid_grant` = lost, anything
  else temporary. Protected by the PAC-67 tests in `backend/auth` and
  `backend/api`. A decryption failure must never be classified as lost.
- **PAC-72 D-6**: unreadable host = busy (AC-18); **D-7**: only a real
  storage failure fails the listing (a decryption failure is not one).
- **PAC-67 disconnect**: removal succeeds whether or not the stored value can
  be read; the Zoom revocation uses the decrypted value
  (`api/handlers_credentials.go:125-147` reads what `removeCredential`
  returns, which becomes ciphertext), and reports `unconfirmed` when it cannot
  be decrypted (AC-16c).
- **PAC-73 key redaction** of provider error text
  (`api/handlers_llm.go:80`, `engine/meeting_brief_service.go:55-59`) must
  still receive the plaintext keys it redacts.
- **PAC-81 operations**: up-only migrations at start, the restore procedure,
  the 14-day retention, readiness semantics. The rehearsal must keep passing
  (updated per AC-25).
- **PAC-71** (if merged first): the operator CLI's existing subcommands, their
  audit records and the `033` migration are unchanged; this work adds
  subcommands and widens the audit `action` values, it does not rename any.
  PAC-71's operator removals write `''` (and NULL for `microsoft_tokens`) to
  inventory columns by SQL (`storage/operator.go:48-57` on that branch);
  that stays correct because empty stays empty (AC-7). **Merge overlap** with
  that branch, to be resolved by whichever merges second: `storage/settings.go`,
  `storage/integrations.go`, `storage/personal_calendars.go`,
  `storage/org_ai.go`, `api/handlers_personal.go`,
  `api/handlers_integrations.go`, `engine/personal_blocker.go`,
  `cmd/paceday-operator/main.go` (+ test), `config/env_reference_test.go`,
  `e2e/seed/seed.sql`, `docs/runbook.md`, and the migration number (`033` is
  PAC-71's).
- **Consumers this breaks, and must be updated in the same change**:
  `e2e/tests/llm-bring-your-own.spec.ts:313-330` (asserts a raw plain column
  after start — the conversion will have encrypted it); the rehearsal's raw
  comparison (`rehearse-backup-restore.sh:166,212`); Go tests that insert by
  SQL and then compare the column raw; every backend boot listed in §2.2
  (dev compose, prod compose, e2e compose, `e2e/tests/startup.spec.ts`
  `BOOTABLE` and the production-reference boot, the rehearsal,
  `scripts/test-backend.sh`) — AC-8b, AC-28; `docs/security.md:15,78,90,102`
  and `docs/runbook.md:247-262`. The e2e seed may keep inserting plain values
  (they are converted at start, D-7).
- **Future migrations** can no longer move a secret between columns or rows
  in SQL (`029_llm_bring_your_own.up.sql:22-25` did exactly that): the
  binding of AC-5 makes such a move unreadable. The plan must record this rule
  where migration authors will read it.

## 7. Rollback

- **Before the conversion has run** (or with it disabled): image-only rollback
  works; the previous release reads plain values.
- **After the conversion**: the previous release cannot read encrypted
  values. Worse, it would send ciphertext to providers as tokens, a refusal
  could be classified `invalid_grant`, and every connection would be marked
  lost. So the runbook gives two ways back, and forbids a plain image swap:
  1. **Restore the pre-deploy backup** (PAC-81 standard rollback), losing data
     written since; the restored dump holds plain values again.
  2. **Prepare-downgrade, then roll back**: run the decrypt-all subcommand
     (AC-21/AC-22), which writes every value back in plain (the exposure
     returns, deliberately and audited), run the schema down step, then
     deploy the previous image. No data lost.
- **The schema down step** refuses while anything is encrypted (AC-22), so it
  cannot strand ciphertext without its data keys. After decrypt-all it drops
  only what this work added; no user data is lost. Order: stop the backend,
  `decrypt-all` (refuses while a backend is connected, AC-21c), down step,
  start the previous image — never start the new image in between, or its
  automatic conversion re-encrypts everything.
- **Operator audit records are never removed or narrowed by the down step.**
  If this work widens PAC-71's `operator_audit.action` CHECK, the down step
  keeps the widened CHECK (narrowing it would fail on, or require deleting,
  the `decrypt-all` record that the downgrade itself just wrote; PAC-71 §7
  already keeps the table on down).
- **Key rotation rollback**: keep the previous key configured until the
  status command shows nothing under it; re-adding it is always safe.

## 8. Open questions

### Owner (product-level)

All three answered by the owner on 2026-11-01 (recorded in
`.claude/factory/owner-decisions.md`); the answers match the factory
defaults, so the body above stands as written.

- **Q-1. Key from the environment now, KMS later?** **Answered: yes —
  `PACEDAY_ENCRYPTION_KEY` from the environment now, a KMS later** (§10
  follow-up). Factory default was: an
  environment key now (fits the self-hosted, single-host deployment PAC-81
  documents; no cloud dependency, consistent with "no ambient cloud identity"
  from the LLM decision); the design keeps a KMS drop-in possible. Answer
  "KMS now" only if a specific KMS is already chosen.
- **Q-2. May losing the key lose every connection?** **Answered: yes —
  recovery is the operator reset (AC-23), then users reconnect and re-enter
  keys.** Factory default was: yes. The
  key is not recoverable from the database by design; the only way forward is
  the lost-key reset (AC-23): every user reconnects Google/Outlook/Zoom/Slack/
  Notion and re-enters AI keys, admins re-enter the org AI key and SSO client
  secrets. Until an admin re-enters the SSO secret, SSO sign-in for that
  domain fails (break-glass `PACEDAY_ADMIN_EMAILS` still works). The
  alternative — an escrow copy of the key in the backup — defeats the point.
- **Q-3. Is the key kept apart from the backups?** **Answered: yes —
  `backup.sh` must not copy it (AC-4), and retired keys are kept at least 14
  days (AC-25).** Factory default was: yes. The
  backup set must not contain the key (AC-4); the operator stores it once,
  separately (password manager or offline), and keeps retired keys at least
  as long as the backup retention (14 days) so any kept backup can be
  restored. Consequence: a restore needs two things from two places. Without
  this, a stolen backup set is still every credential, i.e. the ticket fixes
  nothing for backups.

### Plan stage

- **P-1. Row identity for the binding.** It must be known at write time and
  never change for the row's life. Natural candidates: user id (settings),
  user id + calendar id (Google tokens), user id + provider (workspace
  connections), organisation id (org AI). SSO: **not** the stored domain
  text — an update rewrites it to lower case in place
  (`sso_providers.go:217`); use the row `id` (known only after an insert, as
  below) or `lower(domain)` (what the upserts at `sso_providers.go:81-93` and
  `:232-247` conflict on). Do not include `personal_calendars.user_id`: it is
  nullable (§2.2). Personal calendars use a serial id assigned by the insert
  (`personal_calendars.go:57-63`); S5 is written in that insert, so the plan
  must either generate the id before the insert or write S5 in a second
  statement inside the same transaction.
- **P-2. Migration number and order with PAC-71.** PAC-71 takes `033` on its
  branch; this work's schema step is the next free number at merge. If this
  merges before PAC-71, it creates the operator CLI and the audit table and
  PAC-71 rebases onto them; preferred order is PAC-71 first.
- **P-3. Where the conversion runs**: a background goroutine started after the
  server is ready (D-7) versus only the operator subcommand. Default: both —
  automatic at start, in small batches, plus the subcommand.
- **P-4. Settings reads decrypting secrets nobody asked for** (§2.2): decrypt
  lazily per field or eagerly. AC-26's budget allows either; **AC-16c rules
  out any design where one undecryptable secret fails the whole settings
  read or save, or a whole list** — that is the deciding constraint, not
  speed.
- **P-5. Audit record** for the new subcommands: widen PAC-71's
  `operator_audit.action` CHECK, or a separate table if PAC-71 is not merged.
  Either way the down step never narrows or empties it (§7).
- **P-6. Lock and "backend connected" detection** for AC-21b/AC-21c (e.g. how
  a subcommand knows a backend is running) is the plan's choice; it must work
  for the single-host compose deployment PAC-81 documents.

## 9. Decisions

Factory-assumed unless marked "owner". Each can be overturned at challenge.

- **D-1. Envelope encryption, AES-256-GCM.** A key-encryption key (KEK) from
  the environment wraps a small set of data keys (DEKs) stored in the
  database; values are encrypted with a DEK. One DEK is active for writes at a
  time and serves every inventory column; the stored value carries a format
  version, the DEK id and a fresh random 96-bit nonce. Why not one DEK per
  row: with a future KMS that would be one remote call per value; with a few
  DEKs unwrapped once at start (AC-26), the hot paths (settings reads,
  unauthenticated public booking) cost only local AES-GCM. Why not
  `pgcrypto`: the key would travel to the database server in every query and
  end up in its logs and `pg_stat_statements`. Random 96-bit nonces bound one
  DEK to well under 2^32 encryptions; every settings save re-encrypts its
  non-empty keys and every token renewal re-encrypts, so the plan states the
  expected encryptions per DEK per year for the documented deployment size
  and `encryption status` reports each DEK's age, so `rotate-data-key` can
  also be run routinely, not only on suspected exposure.
- **D-2. Associated data** binds every value to (table, column, row identity,
  format version) so values cannot be swapped between rows or columns
  (AC-5). Row identity per P-1.
- **D-3. Storage format**: values stay in their existing `TEXT` columns as a
  versioned, prefixed text encoding (no column type change, nullable and
  default-empty columns keep working). The empty string is stored as the
  empty string (AC-7): presence is not a secret, and every existing
  "connected?" check depends on it. Accepted leaks: presence, and the
  approximate length of a value (GCM does not pad) — neither lets anyone act
  as the user. The prefix that marks an encrypted value must be one no plain
  value in the inventory can start with (provider tokens, JSON records,
  `https://`/`webcal://` URLs, PEM, API keys), so a legacy plain value is
  never mistaken for ciphertext; the plan names it and a test pins that every
  plain value form in the e2e seed is recognised as plain.
- **D-4. Environment variables** (names factory-assumed):
  `PACEDAY_ENCRYPTION_KEY` (required) = `<key id>:<base64 of 32 random
  bytes>`, generated with e.g. `openssl rand -base64 32`, the id chosen by the
  operator (e.g. a date); `PACEDAY_ENCRYPTION_KEY_PREVIOUS` (optional,
  comma-separated, same format) during a rotation. Documented in
  `.env.example` with `#:` lines (PAC-81 Part D format), kept out of the file
  the backup copies (D-9).
- **D-5. Start without a valid key refuses**, always — including a fresh
  install with nothing encrypted yet — like `JWT_SECRET` (`main.go:248-250`).
  A per-request 500 instead would leave a deployment that looks up but
  silently fails every connected feature and keeps writing plain values. The
  start check unwraps every DEK; any failure refuses (AC-8..AC-10). The check
  runs before any cron is scheduled, before the automatic conversion and
  before listening (`main.go` today schedules crons before its `JWT_SECRET`
  check, §2.2). Development and tests use committed test-only keys (AC-28).
- **D-6. KEK rotation = re-wrap DEKs at start**: when DEKs are wrapped by a
  previous KEK, the backend re-wraps them under the active KEK in one
  transaction at start (a handful of rows, idempotent). No inventory row is
  rewritten. **DEK rotation** (needed only if a DEK is suspected exposed) is
  the operator subcommand of AC-11b, re-encrypting values in batches.
- **D-7. Conversion is online, lazy-tolerant and idempotent**: reads accept a
  plain value forever (a value without the encrypted prefix is plain); every
  write encrypts; a conversion pass encrypts remaining plain values in small
  batches, each row updated only if its value has not changed since it was
  read (AC-13) — compared without sending the plaintext to the server
  (AC-19c) — so it is resumable and safe beside live traffic. The conversion
  writes only the secret column (AC-12) and runs under the lock of AC-21b. Accepting plain
  reads permanently is deliberate: someone who can write the database already
  controls the deployment, and it keeps the e2e seed and hand-written repair
  SQL working. The status command makes leftovers visible (AC-15, AC-20).
- **D-8. Operator subcommands** on `paceday-operator` (PAC-71's CLI):
  `encryption status`, `encryption convert`, `encryption rotate-data-key`,
  `encryption decrypt-all` (prepare downgrade), `encryption reset-lost-key`.
  Mutating ones require `--operator` and `--reason` and write the operator
  audit record, as PAC-71's do.
- **D-9. Backups never contain the key** (owner Q-3, answered 2026-11-01). The backup
  script refuses an env file that contains it (AC-4) rather than silently
  stripping it, so the operator learns where the key must live. Compose reads
  the key from a separate env file next to `.env` that the backup does not
  copy (plan decides the exact mechanism, within AC-8b: the e2e
  production-reference boot must still see the key).
- **D-10. A decryption failure is a server error, not a lost credential**
  (AC-16..AC-18): it is a deployment or tampering problem, never the user's
  doing; marking the connection lost would tell the user to reconnect and
  would hide the cause. Status paths keep their existing mapping (AC-16b),
  and the user can always replace or remove the value (AC-16c), so a single
  damaged row never strands a user until an operator acts.
- **D-11. Logging**: never the value, the KEK, a DEK, or ciphertext; failure
  lines carry table, column, row identity, DEK id, KEK id. Error values from
  the crypto layer are constructed without input bytes.
- **D-12. Scope includes webcal URLs (S5, S6) and the SAML certificate
  (S11).** A private iCal address is a bearer credential to a calendar; the
  SAML certificate is public, but PAC-22 already treats it as never leaving
  the server, its integrity matters, and including it costs nothing.
- **D-13. Owner decisions honoured**: LLM keys write-only (PAC-24); org AI key
  under the same rules as personal keys (PAC-73); PAC-81 runbook, env
  reference format and "rollback by restore" (Q-O3 retention 14 days).

## 10. Follow-up tickets proposed

- **Store team invitation tokens hashed** (looked up by value; §2.1).
- **KMS-backed key-encryption key** (owner Q-1, 2026-11-01: "KMS later").
- **Revoke provider tokens present in pre-upgrade backups** — only if the
  owner wants more than letting them age out.

## Challenge — 2026-11-01

**Verdict**: accept-with-changes. Every change below has been applied to the
body above (factory instruction for this run: fix the spec directly). Owner
answers Q-1..Q-3 (2026-11-01) applied; they match the factory defaults.

Citations checked against `origin/main` `953f96e` and PAC-71
`origin/impl/pac-71-identity-binding-backend` `559b6c1`.

### Challenge log

| # | Issue | Severity | Verdict | Change |
|---|---|---|---|---|
| C-1 | AC-16 promised a generic 500 on every path, but §2.2's paths already map read errors four different ways (status → "not connected", best-effort → skip, host availability → busy, lists → whole 500). The spec did not say which survives, so AC-16 was not implementable as written and "the API returns exactly what it returns today" contradicted it. | blocking | fixed | §2.2 enumerates the mappings; AC-16 limited to paths that need the secret to act; AC-16b keeps the status mappings and states presence flags stay "set". |
| C-2 | No recovery path for one damaged value: with an eager settings read, one undecryptable AI key would 500 every settings read *and* every settings save (the save re-reads and rewrites all six keys), so the user could not even remove it; one bad personal calendar would fail the whole list and that user's blocker run. | blocking | fixed | New AC-16c (settings read/save, replace, disconnect, delete keep working); AC-17 made per item; P-4 now decided by AC-16c, not speed. |
| C-3 | AC-23 lost-key reset "empties every secret". Emptying `oauth_tokens` or `workspace_connections` rows leaves them reading as **connected** (`handlers_auth.go:156`, `handlers_integrations.go:99`); credential-loss rows, Zoom/Teams conferencing and enabled webcal calendars with empty URLs were left inconsistent; "nothing else changes" contradicted what a disconnect does. | blocking | fixed | AC-23 rewritten: each secret ends as the owner's own removal leaves it, losses cleared, no provider call. |
| C-4 | Fresh install refuses without a key (D-5), but `e2e/tests/startup.spec.ts` `BOOTABLE` would then make every refusal case pass for the wrong reason (it checks only "refusing to start"), the production-reference boot reads only compose's `environment:` block, and dev compose / rehearsal / Go suite had no stated key source. | blocking | fixed | §2.2 lists every boot; new AC-8b and AC-28; D-5 names test-only keys; D-9 constrained by AC-8b. |
| C-5 | Conversion "no value changed" did not forbid touching `oauth_tokens.updated_at`, which decides which token `LoadUserToken` returns, nor `settings.updated_at`, `configuration_version`, etc. | high | fixed | AC-12 requires no other column of a converted row to change. |
| C-6 | Concurrency of the operator subcommands: automatic conversion vs `rotate-data-key` vs `decrypt-all` in parallel, and `decrypt-all` while a backend runs (the backend re-encrypts behind it; its start-up conversion re-encrypts everything). Also two processes creating the first DEK on a fresh database. | high | fixed | AC-21b (one at a time), AC-21c (refuse while a backend is connected), AC-27 (one active DEK), §7 order of the downgrade, P-6. |
| C-7 | Startup order: crons are scheduled before the `JWT_SECRET` check (`main.go:168` vs `:248`), so "refuse like JWT_SECRET" did not guarantee no background work runs without a key. | medium | fixed | AC-8 and D-5: refuse before crons, conversion and listening. |
| C-8 | AAD row identity for SSO by stored domain: an update rewrites `domain = lower(domain)` in place (`sso_providers.go:217`). `personal_calendars.user_id` is nullable. | medium | fixed | P-1: SSO by `id` or `lower(domain)`; personal calendars not by user id. |
| C-9 | Down step vs PAC-71's `operator_audit`: narrowing the widened `action` CHECK on down fails on (or deletes) the very `decrypt-all` record the downgrade writes. Down step command unnamed (the backend only migrates up). | medium | fixed | §7 keeps the widened CHECK; AC-22 requires the runbook to name the command and restates the "previous release reads" half as a testable schema + plaintext check. |
| C-10 | The compare-and-set conversion as worded ("still holds the plain value it read") sends each plaintext secret to Postgres as a parameter, which slow/failed-statement logging can persist after the conversion. | medium | fixed | AC-19c; D-7 reworded. |
| C-11 | AC-4 covered only the active key; the previous-key variable is key material too. "Contains the key" was ambiguous for commented/empty lines. AC-24 let `.env.example` carry a value line that operators copy into the `.env` the backup then refuses. | medium | fixed | AC-4 and AC-24 tightened. |
| C-12 | Disconnect path not listed: `removeCredential` returns the raw value and `revokeZoom` decodes it — after encryption it receives ciphertext. | medium | fixed | §2.2 and §6 name it; AC-16c(c). |
| C-13 | AC-26 "p99 under 1 ms compared with the same row read today" mixes DB I/O noise into a sub-millisecond bound on a shared CI runner — not reliably falsifiable. | medium | fixed | AC-26: decryption-only Go benchmark, absolute bound; KEK-use counter over requests + a cron tick. |
| C-14 | AC-18 did not say that a decryption failure is not a PAC-72 D-7 storage failure; an implementation wrapping it as one would 500 the whole listing. | low | fixed | AC-18 and §6. |
| C-15 | S2 is nullable; AC-7 spoke only of the empty string (PAC-71 writes NULL). | low | fixed | AC-7 and inventory row. |
| C-16 | Random-nonce GCM usage bound and DEK age not mentioned; DEK rotation framed as exposure-only. Length leakage and prefix ambiguity (a legacy plain value starting with the marker) not addressed. | low | fixed | D-1 and D-3 notes. |
| C-17 | Team invitation tokens deferred: checked — acceptable, because accepting requires the invitee's signed-in address and invites expire in 7 days. | low | accepted | Rationale added to §2.1. |
| C-18 | Inventory completeness: grep of every `CREATE TABLE`/`ADD COLUMN` in `001`–`032` and PAC-71 `033`, storage writers, the MCP server: no further secret. AI destination URLs refuse userinfo/query; meeting-brief caches hold content, not credentials; audit details carry no token material. | — | no change needed | "Examined and not in the inventory" extended with the evidence. |
| C-19 | PAC-71 overlap was stated only for the CLI and migration number. | low | fixed | §6 lists the overlapping files and confirms PAC-71's SQL removals stay correct (empty stays empty). |

### Citations I checked and found wrong

- `docs/security.md:87` as open risk 4 — actually `:90`; risk 10 (`:102`) and
  the asset rows `:15`, `:78` also need updating. Fixed.
- "`GetOrCreateSettingsByUser` has 25 call sites" — 22 non-test call sites.
  Fixed.
- All other file:line citations in §2 match `953f96e`.

### Criteria I could not falsify (before the edits)

- AC-16 (one envelope for every path — contradicted by existing tests that
  pin "not connected" on read errors). Replaced by AC-16/16b/16c.
- AC-22 "the previous release reads every secret" at `[unit]` level — no unit
  test runs the previous release. Restated as schema + plaintext equality.
- AC-26 p99 delta against a DB read on a shared runner. Restated.

### Consumers this breaks

- clockwise-like `e2e/tests/startup.spec.ts:36-40,72-90` — every boot needs a
  key (AC-8b).
- clockwise-like `e2e/tests/llm-bring-your-own.spec.ts:326-330` — raw column
  read.
- clockwise-like `scripts/ops/rehearse-backup-restore.sh:132,166,212` — boot
  env and raw comparison.
- clockwise-like `scripts/ops/backup.sh:87` — must refuse a key-bearing env
  file.
- clockwise-like `docker-compose.yml`, `docker-compose.prod.yml`,
  `e2e/docker-compose.e2e.yml`, `scripts/test-backend.sh` — key source.
- clockwise-like `backend/api/handlers_credentials.go:125-147` — revocation
  reads the removed value.
- clockwise-like `docs/security.md:15,78,90,102`, `docs/runbook.md:247-262`.
- smart-calendar-flow: none — no HTTP operation changes shape (AC-16b keeps
  status answers as they are).

### Remaining owner questions

None. Q-1..Q-3 answered 2026-11-01; every change above is an engineering
reading of those answers.
