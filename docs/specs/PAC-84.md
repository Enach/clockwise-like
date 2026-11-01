# Spec — PAC-84: Encrypt stored provider credentials and API keys at rest

- **Linear**: https://linear.app/paceday/issue/PAC-84/encrypt-stored-provider-credentials-and-api-keys-at-rest
- **Status**: draft
- **Author**: spec-author
- **Base**: `origin/main` at `953f96e`. Citations are to that commit unless a
  branch is named.
- **Inherited scope**:
  - PAC-81 finding **F-D5** (`docs/specs/NEW-deploy-hardening.md:366`, §10
    follow-up at `:1181-1182`): dumps hold provider credentials in plain text.
    **Resolved by this spec.**
  - `docs/security.md:87` (open risk 4: "LLM provider API keys … in plaintext
    `settings` columns"). **Resolved** for every column in §2.1; SMTP
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
| S2 | Microsoft (Outlook) OAuth token record — JSON of access, refresh, expiry | `settings.microsoft_tokens` — `003_calendar_providers.up.sql:2` (nullable) | connect `auth/microsoft_oauth.go:70-93`; renewal `storage/credentials.go:171-173` via `auth/credential_lifecycle.go:224-228`; remove `credentials.go:190-195` | `microsoft_oauth.go:113-127`; **every settings read** `storage/settings.go:255` (§2.2); `calendar/client_factory.go:17-20` |
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
  (§5).
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

### 2.2 How the secrets travel today

- One settings read loads S2, S3, S6 and all six S8 keys whether or not the
  caller needs them: `settingsColumns` (`settings.go:240-261`) is used by
  `GetOrCreateSettingsByUser` (`:266-307`), which has 25 call sites across
  `api/`, `engine/` and `nlp/` (request handlers and every cron).
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
  tests under `backend/storage` and `backend/api` that insert tokens by SQL.

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
contains the key (the env file copy does not hold it); and the backup script
refuses — exit non-zero, naming the runbook section, writing no file — when
the env file it is told to copy contains the key variable with a value. [ops]

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
connected), then it is stored as the empty string, and every presence check
listed in §2.2 gives the same answer as today: the existing tests that pin
"not connected", `has_*` flags, "renewal does not re-store after disconnect"
(PAC-67) and "empty refresh token keeps the stored one" pass unchanged. [unit]

### Key handling at start

AC-8. Given no key configured, when the backend starts, then it exits
non-zero before listening, with a message naming the key variable and the
runbook section — on a fresh database as well as on one holding encrypted
data (D-5). [e2e]

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
in every inventory column, with no user reconnecting and no value changed.
[e2e]

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
when that user's request needs it, then the response is the generic JSON
server-error envelope (no detail), the server log records the table, column,
row identity, data-key id and the user id — and no value; and the credential
is **not** deleted, **not** marked lost (no credential-loss record, no
`connection_lost` activity entry), and no call is made to the provider with
it. [unit]

AC-17. Given a background job (auto-schedule, auto-decline, personal-blocker,
daily recap, meeting briefs) iterating users, when one user's secret fails to
decrypt, then that user is skipped with the AC-16 log line and every other
user is processed. [unit]

AC-18. Given a public booking link whose host's stored calendar credential
fails to decrypt, then that host counts as unreadable — busy, slots refused —
exactly as PAC-72 D-6 treats an unreadable calendar, never as free. [unit]

### No secret in logs, reports or responses

AC-19. Given a test run of AC-1, AC-5, AC-6, AC-9, AC-12, AC-16 and the
operator commands with the log output and the error-reporter transport
captured, then neither capture contains any canary or the key, in the
encodings of AC-1, nor any stored ciphertext. [unit]

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

### Downgrade, rollback and lost key

AC-22. Given an encrypted deployment, when the schema down step of this work
runs while any encrypted value or data key exists, then it fails without
changing anything, with a message pointing to the prepare-downgrade
subcommand; after that subcommand has written every value back in plain, the
down step succeeds and the previous release reads every secret. [unit]

AC-23. Given the key is lost, when the operator runs the lost-key reset with a
new key configured, then every inventory secret is emptied, every data key is
removed, the backend then starts with the new key, each affected user sees the
affected connections as not connected (not as "lost"), organisations see no
org AI key and no SSO client secret, and the audit record lists the counts.
Nothing else in the database changes. [unit]

### Environment reference, runbook, performance

AC-24. Given `.env.example`, then it documents the active key and the
previous-keys variables in the PAC-81 `#:` line format (status, default,
purpose), its SECRETS paragraph says the key is kept apart from the backup
set, and `backend/config/env_reference_test.go` passes; the e2e stack sets a
test-only key. [unit]

AC-25. Given `docs/runbook.md`, then it has a section on the encryption key
(generate, where to keep it, back it up separately, rotate, retire a previous
key only after the backup retention has passed, restore, what losing it
costs, the lost-key reset), the "Backups are secrets" text no longer says
dumps hold credentials in plain text but says what a dump from before the
conversion still holds, and the restore rehearsal
(`scripts/ops/rehearse-backup-restore.sh`) supplies the key separately from
the backup set and proves a seeded credential reads back through the backend
after restore — not by comparing raw columns. [ops]

AC-26. Given the backend at start, then the key-encryption key is used only to
unwrap data keys at start (the number of unwrap operations equals the number
of stored data keys; zero per request), and a benchmark of reading one
settings row with all its secrets filled shows decryption adding under 1 ms
at p99 on the CI runner compared with the same row read today. [unit]

## 5. Explicitly out of scope

- **A key-management service** (cloud KMS, Vault). The key comes from the
  environment; the design keeps one place where the key-encryption key is
  used so a KMS can replace it later (D-1). Owner question Q-1.
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
- **PAC-72 D-6**: unreadable host = busy (AC-18).
- **PAC-73 key redaction** of provider error text
  (`api/handlers_llm.go:80`, `engine/meeting_brief_service.go:55-59`) must
  still receive the plaintext keys it redacts.
- **PAC-81 operations**: up-only migrations at start, the restore procedure,
  the 14-day retention, readiness semantics. The rehearsal must keep passing
  (updated per AC-25).
- **PAC-71** (if merged first): the operator CLI's existing subcommands, their
  audit records and the `033` migration are unchanged; this work adds
  subcommands and widens the audit `action` values, it does not rename any.
- **Consumers this breaks, and must be updated in the same change**:
  `e2e/tests/llm-bring-your-own.spec.ts:313-330` (asserts a raw plain column
  after start — the conversion will have encrypted it); the rehearsal's raw
  comparison (`rehearse-backup-restore.sh:166`); Go tests that insert by SQL
  and then compare the column raw. The e2e seed may keep inserting plain
  values (they are converted at start, D-7).
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
  only what this work added; no user data is lost.
- **Key rotation rollback**: keep the previous key configured until the
  status command shows nothing under it; re-adding it is always safe.

## 8. Open questions

### Owner (product-level)

- **Q-1. Key from the environment now, KMS later?** Factory default: an
  environment key now (fits the self-hosted, single-host deployment PAC-81
  documents; no cloud dependency, consistent with "no ambient cloud identity"
  from the LLM decision); the design keeps a KMS drop-in possible. Answer
  "KMS now" only if a specific KMS is already chosen.
- **Q-2. May losing the key lose every connection?** Factory default: yes. The
  key is not recoverable from the database by design; the only way forward is
  the lost-key reset (AC-23): every user reconnects Google/Outlook/Zoom/Slack/
  Notion and re-enters AI keys, admins re-enter the org AI key and SSO client
  secrets. Until an admin re-enters the SSO secret, SSO sign-in for that
  domain fails (break-glass `PACEDAY_ADMIN_EMAILS` still works). The
  alternative — an escrow copy of the key in the backup — defeats the point.
- **Q-3. Is the key kept apart from the backups?** Factory default: yes. The
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
  connections), organisation id (org AI), provider domain (SSO — check that a
  domain is never renamed in place; `sso_providers.go:81-93` upserts by
  domain). Personal calendars use a serial id assigned by the insert
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
  lazily per field or eagerly. AC-26's budget decides; eager is simpler.
- **P-5. Audit record** for the new subcommands: widen PAC-71's
  `operator_audit.action` CHECK, or a separate table if PAC-71 is not merged.

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
  end up in its logs and `pg_stat_statements`.
- **D-2. Associated data** binds every value to (table, column, row identity,
  format version) so values cannot be swapped between rows or columns
  (AC-5). Row identity per P-1.
- **D-3. Storage format**: values stay in their existing `TEXT` columns as a
  versioned, prefixed text encoding (no column type change, nullable and
  default-empty columns keep working). The empty string is stored as the
  empty string (AC-7): presence is not a secret, and every existing
  "connected?" check depends on it.
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
  start check unwraps every DEK; any failure refuses (AC-8..AC-10).
- **D-6. KEK rotation = re-wrap DEKs at start**: when DEKs are wrapped by a
  previous KEK, the backend re-wraps them under the active KEK in one
  transaction at start (a handful of rows, idempotent). No inventory row is
  rewritten. **DEK rotation** (needed only if a DEK is suspected exposed) is
  the operator subcommand of AC-11b, re-encrypting values in batches.
- **D-7. Conversion is online, lazy-tolerant and idempotent**: reads accept a
  plain value forever (a value without the encrypted prefix is plain); every
  write encrypts; a conversion pass encrypts remaining plain values in small
  batches, each row updated only if it still holds the plain value it read
  (AC-13), so it is resumable and safe beside live traffic. Accepting plain
  reads permanently is deliberate: someone who can write the database already
  controls the deployment, and it keeps the e2e seed and hand-written repair
  SQL working. The status command makes leftovers visible (AC-15, AC-20).
- **D-8. Operator subcommands** on `paceday-operator` (PAC-71's CLI):
  `encryption status`, `encryption convert`, `encryption rotate-data-key`,
  `encryption decrypt-all` (prepare downgrade), `encryption reset-lost-key`.
  Mutating ones require `--operator` and `--reason` and write the operator
  audit record, as PAC-71's do.
- **D-9. Backups never contain the key** (owner Q-3 default). The backup
  script refuses an env file that contains it (AC-4) rather than silently
  stripping it, so the operator learns where the key must live. Compose reads
  the key from a separate env file next to `.env` that the backup does not
  copy (plan decides the exact mechanism).
- **D-10. A decryption failure is a server error, not a lost credential**
  (AC-16..AC-18): it is a deployment or tampering problem, never the user's
  doing; marking the connection lost would tell the user to reconnect and
  would hide the cause.
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
- **KMS-backed key-encryption key**, if Q-1 is answered "later".
- **Revoke provider tokens present in pre-upgrade backups** — only if the
  owner wants more than letting them age out.
