# Contract: PAC-84 (encrypt stored provider credentials and API keys at rest)

- **Linear**: PAC-84, https://linear.app/paceday/issue/PAC-84/encrypt-stored-provider-credentials-and-api-keys-at-rest
- **Spec**: `docs/specs/PAC-84.md`, challenged accept-with-changes 2026-11-01
  (C-1..C-19 applied), owner Q-1..Q-3 answered 2026-11-01. Branch
  `factory/pac-84-spec` at `3d776ba`.
- **Stage**: contract-author, revision 1, 2026-11-02; contract-challenger
  2026-11-02 (accept-with-changes, K-1..K-17 applied in place, log at the
  end). Next: planner.
- **Manifest**: `contracts/features/PAC-84.yaml`.
- **Owner decisions applied** (`.claude/factory/owner-decisions.md`, PAC-84,
  2026-11-01): Q-1 key from the environment now, KMS later; Q-2 a lost key
  may lose every connection, recovery is an operator reset and users
  reconnect; Q-3 the key is kept apart from backups and retired keys are kept
  at least 14 days.

Citations are to `origin/main` `953f96e` unless a branch is named. PAC-71
citations are to `origin/impl/pac-71-identity-binding-backend`.

Verification at this stage: `py -3 scripts/openapi_assemble.py --check` ->
`OK: bundle is in sync (106 paths)`; `py -3 scripts/openapi_migration_report.py
--check` -> `OK: MIGRATION.md covers all 128 operations (10 generated)`;
`bash scripts/openapi_gen_go.sh --check` -> `OK: backend/api/gen matches the
contract` (all rc 0; `make` is not installed on this host, so the three
`openapi-check` steps were run one by one; the web half was not run because the
bundle does not change). Nothing was implemented or observed running: every
statement about behaviour after PAC-84 is what stage 4 must build.

## 1. HTTP: no change

**API change: no.** No path, operation, schema, status or description in
`contracts/openapi/` changes, so the bundle stays byte-identical:
`contractHash` stays `a15d981e14f5bd8e5dbc1ff206952a8dbcd9e02d160dcc2ee31135b9808ee472`
(revision 3.1, the same as PAC-72's), and the web repo regenerates nothing.

Why that is true, checked against the bundle rather than assumed:

- **AC-16 (act paths answer 500).** Every operation that needs a stored
  secret to act already documents the shared `InternalError`
  (`{"error":"internal error"}`). A script over the bundle lists the 20
  operations with no documented 500; none of them acts with a stored secret
  inside the request: start/connect redirects (no stored secret), status
  operations (AC-16b keeps their mapping), disconnects (AC-16c keeps 200),
  `recomputeAnalytics` and `reoptimizeHabits` (answer before a detached unit
  runs, `handlers_analytics.go:92-101`, `handlers_habits.go:381-389`),
  `getCurrentUser`, `logout`, health/readiness, templates, manager removal.
- **AC-16b/16c**: status answers, `*Set`/`has*` flags, settings GET/PATCH and
  personal-calendar list keep their documented shapes and values.
- **AC-18**: public booking keeps PAC-72's 200/409 semantics; a decryption
  failure is "unreadable host", already covered by D-6 text.
- **AC-19b**: S5/S6 are returned to their owner with the same value.

Two server mappings stage 4 must get right *without* a contract change
(the contract already says 500; today's code would say something else):

1. **SSO start and callback.** `handlers_sso.go:57-61` and `:122-125` turn
   any error from `GetSSOProviderByDomain` into 404 "SSO not configured". A
   decryption failure must not reach that branch: the provider read must
   succeed with the secret undecrypted, and the handler answers the documented
   500 when it tries to use it (section 9.3). A 404 would tell the admin the
   domain is not configured, which is false.
2. **AI paths.** An undecryptable AI key (S8, or the org key S9 a member's
   request runs on) is the documented 500, never a silent fall-back to "no
   key configured" behaviour. It is decided where the AI configuration is
   decided, **before** anything that turns a failure into an answer:
   `testLLMConnection` 500 ("Settings load failure", never its 409
   `AINotConfigured`), `parseNaturalLanguage` 500 ("settings could not be
   loaded", never the 200 `not_configured` and never the parser's 200
   `{"intent":"unknown"}` swallow), and likewise every AI-backed operation.
3. **Conferencing (challenge K-2).** `handlers_conferencing.go:52-63` turns
   a Zoom decode failure into `ErrZoomInvalidCredential` and any
   `LoadMicrosoftToken` error into `errTeamsNotConnected`, both 400s of the
   closed refusal list (`:74-83`). A decryption failure is neither: it is the
   operation's documented 500, checked with `errors.Is(err,
   secretbox.ErrDecrypt)` before the refusal mapping.
4. **Daily recap test send (K-2).** `handlers_daily_recap.go:159-162` turns
   any `GetWorkspaceConnection` error into 400 "Slack not connected". A
   decryption failure of the Slack token is the operation's documented 500
   (whose documented body is `{"error":"db error"}`, not `internal error`).
5. **Personal calendar fetches (K-2).** `handlers_personal.go:147-155` and
   `:207-210` turn any `GetPersonalCalendar` error into 404 "personal calendar
   not found". By the read rule of section 6.2 the fetch never fails on an
   undecryptable field, so these paths answer as for any other row (the `url`
   is omitted, section 9.3).

The general rule behind 1-5 (section 6.2): **a storage read function never
returns a decryption failure as its error**; the failure surfaces only where
a caller opens that one field, and every caller that maps "not connected /
not configured / not found" checks `errors.Is(err, secretbox.ErrDecrypt)`
first. AC-16's body is the operation's **documented** 500 body.

If the challenger finds an act path whose documented answer is not 500, the
fix is a description-only contract revision, not a new status (spec §5).

## 2. Inventory: the 18 secret columns

S1–S11 of the spec map to these columns. This list is the registry
`storage` iterates for conversion, status, rotation, decrypt-all and reset;
a column is added to the inventory only by adding it here (a later contract).

| # | table.column | Nullable | Row identity (section 3.3) |
|---|---|---|---|
| S1 | `oauth_tokens.access_token`, `oauth_tokens.refresh_token` | no | `user_id`, `calendar_id` |
| S2 | `settings.microsoft_tokens` (whole JSON text) | **yes** | `user_id` |
| S3 | `settings.zoom_tokens` (whole JSON text) | no | `user_id` |
| S6 | `settings.webcal_url` | no | `user_id` |
| S8 | `settings.llm_api_key`, `llm_api_key_openai`, `llm_api_key_anthropic`, `llm_api_key_azure_openai`, `llm_api_key_bedrock`, `llm_api_key_vertex` | no | `user_id` |
| S4 | `personal_calendars.credentials_json` (whole JSON text) | no | `id` |
| S5 | `personal_calendars.url` (every provider, not only webcal) | no | `id` |
| S7 | `workspace_connections.access_token`, `workspace_connections.bot_token` | no | `user_id`, `provider` |
| S9 | `org_ai_configurations.api_key` | no | `org_id` |
| S10 | `sso_providers.oidc_client_secret` | no | `id` |
| S11 | `sso_providers.saml_cert` | no | `id` |

JSON-blob columns (S2, S3, S4) are sealed as one unit: the plaintext is the
exact text the column holds today; no SQL reads inside them (checked: no
`::json`/`->>` on these columns in non-test code), so nothing in SQL needs
their structure.

S5 is the whole column regardless of `provider` (factory-assumed: one rule per
column is simpler to test and to convert than a provider-conditional one; a
non-webcal URL costs nothing to encrypt). Settings has nine secrets
(S2, S3, S6, six S8), the "nine filled" of AC-26.

## 3. Ciphertext envelope and associated data

### 3.1 Stored text

```
pdenc:1:<dek>:<body>
```

- `pdenc:` is the **marker**. A stored value is classified by its first
  bytes only:
  - `NULL` or `''` -> **empty** (stored and returned as is; never sealed);
  - starts with `pdenc:` -> **sealed**;
  - anything else -> **plain** (D-7: accepted on read forever).
- `1` is the format version (ASCII decimal). Any other version, including a
  missing one, is a decryption error with reason `unsupported_version`, never
  "plain".
- `<dek>` is the data-key id, ASCII decimal, 1..2147483647, no sign, no
  leading zero.
- `<body>` is base64url (RFC 4648 §5) **without padding** of
  `nonce (12 bytes) || ciphertext || GCM tag (16 bytes)`; decoded length
  >= 28.
- Exactly four `:`-separated fields (base64url has no `:`). Anything that
  starts with `pdenc:` and does not parse is reason `malformed`.
- Cipher: AES-256-GCM (Go `crypto/aes` + `cipher.NewGCM`, standard 12-byte
  nonce, 16-byte tag), nonce from `crypto/rand` per seal. Plaintext is the Go
  string's bytes exactly (no normalisation, no trimming).

Why `pdenc:` cannot collide with a plain value (D-3): plain forms are Google
`ya29.`/`1//` tokens, Microsoft `EwB…`/JWT `eyJ…`, Slack `xoxp-`/`xoxb-`,
Notion `secret_`/`ntn_`, JSON `{`, `https://`/`webcal://` URLs, PEM `-----`,
API keys (`sk-`, `sk-ant-`, `AKIA`, Azure hex, Vertex JSON `{`). None begins
with `pdenc:`; a value that did would read as a decryption error, not as the
wrong plaintext. Stage 4 pins this with a test that classifies every secret
value in `e2e/seed/seed.sql` and one sample of each form above as **plain**.

**Implementation note (stage-4 review #5, 2026-11-02).** AC-6 requires that
changing any single byte of a stored value, the marker included, fails to
decrypt. So a value whose first six bytes differ from `pdenc:` in exactly
one place and whose rest is `<digits>:<digits>:<base64url>` is classified
**sealed (malformed)**, not plain. Go (`secretbox.Classify`) and SQL
(`storage.DamagedSQL`, the down step's `DO` block) apply the same rule.
Such a value:

- is counted `malformed` by `status`, which is neither plain nor encrypted,
  so `--check` does not fail on it;
- is never selected by `convert`;
- is reported by `decrypt-all`, and removed with `--empty-undecryptable`;
- blocks the data-key removal and the downgrade;
- is removed by `reset-lost-key`.

### 3.2 Associated data (AAD)

Every seal and open passes this byte string as GCM additional data:

```
"paceday/secret" 0x00
0x01                                  format version (u8)
lp16(table) lp16(column)
u8 n                                  number of row-identity parts (1 or 2)
n x ( lp16(part name) lp16(part value) )
```

`lp16(x)` = 2-byte big-endian length, then the bytes of `x`. Length-prefixing
makes the encoding injective (no two bindings share bytes). A table, column,
part name or part value longer than 65535 bytes is an error of `Seal`/`Open`
(never truncated); no inventory identity comes near it. The data-key id is
not in the AAD: a changed id selects another key and fails authentication
anyway (AC-6).

### 3.3 Row identity per table (P-1 resolved)

Values are canonical text: UUIDs lower-case hyphenated 36 chars; integers
base-10, no sign, no leading zero; text exactly as stored.

| Table | Parts, in this order | Why |
|---|---|---|
| `oauth_tokens` | `user_id`, `calendar_id` | The upsert key (`token_store.go:25-33`, unique `(user_id, calendar_id)`, `017_…:11`); both NOT NULL and never updated. A copy between two calendars of one user fails. |
| `settings` | `user_id` | NOT NULL + unique since `018`/`024`; never updated. |
| `workspace_connections` | `user_id`, `provider` | The upsert conflict key (`UNIQUE (user_id, provider)`, `014_…:12`); `id` is not known to the upsert's caller. |
| `org_ai_configurations` | `org_id` | Primary key (`031_…:18`). |
| `sso_providers` | `id` | **Not the domain.** An update rewrites `domain = lower(domain)` in place (`sso_providers.go:217`), and Go `strings.ToLower` and Postgres `lower()` may disagree outside ASCII; `id` never changes, so lower-casing is irrelevant to the binding. |
| `personal_calendars` | `id` | `user_id` is nullable (`018`), so it is not used; `id` is the serial primary key. |

**Writing a value whose row id is assigned by the insert** (`sso_providers`
upserts at `sso_providers.go:81-93,232-247`, `personal_calendars` insert at
`personal_calendars.go:57-63`): the row statement and the secret statement
run **in one transaction**. Either the id is obtained first
(`nextval(pg_get_serial_sequence('personal_calendars','id'))`, or a Go
`uuid.New()` for a plain insert) and the insert carries the sealed value, or
the upsert writes the row without changing the secret column(s) and a second
`UPDATE … SET <secret> = $sealed WHERE id = $id` (id from `RETURNING id`)
follows. Required either way: no committed state in which a secret column holds
plaintext or a value sealed for another row; an upsert that hits the conflict
row seals for **that** row's `id`. The plan picks per call site.

**Consequence (spec §6):** a migration can no longer move a secret between
rows or columns in SQL (as `029_…:22-25` did). The plan records this in the
migrations directory's README or header comment.

## 4. Data keys

### 4.1 Table

```sql
CREATE SEQUENCE encryption_data_keys_id_seq AS integer MINVALUE 1;
CREATE TABLE encryption_data_keys (
    id          INTEGER     PRIMARY KEY DEFAULT nextval('encryption_data_keys_id_seq'), -- never reused
    state       TEXT        NOT NULL CHECK (state IN ('active', 'retired')),
    kek_id      TEXT        NOT NULL CHECK (kek_id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$'),
    wrapped     BYTEA       NOT NULL CHECK (octet_length(wrapped) = 60),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    wrapped_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at  TIMESTAMPTZ NULL,
    sealed_count BIGINT     NOT NULL DEFAULT 0 CHECK (sealed_count >= 0), -- seals under this key (K-8)
    CHECK ((state = 'retired') = (retired_at IS NOT NULL))
);
ALTER SEQUENCE encryption_data_keys_id_seq OWNED BY encryption_data_keys.id;
CREATE UNIQUE INDEX encryption_data_keys_one_active
    ON encryption_data_keys ((true)) WHERE state = 'active';

CREATE TABLE encryption_backend_heartbeats (
    instance_id UUID        PRIMARY KEY,           -- random per backend process
    hostname    TEXT        NOT NULL DEFAULT '',
    active_dek  INTEGER     NULL,                  -- the data key this process seals with; NULL until its first key load (K-3)
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    seen_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- **At most one active data key** (partial unique index; AC-27). Retired keys
  are **kept** and still unwrapped at start, so a value written under a key
  just retired (by a backend that had not yet switched) stays readable.
  Rows are removed only by `reset-lost-key` (all) and `decrypt-all` (all,
  once nothing is sealed). Factory-assumed: the spec says a rotated-out key
  "can then be retired"; deleting it was rejected because a backend that had
  not yet seen the rotation could still seal under it (section 13, alternative 4).
- The id sequence is never reset, so a data-key id is never reused within a
  database's life (a restored dump brings its own table and sequence).
- **Nonce bound and rotation trigger (O-2, K-8).** Nonces are 96-bit random,
  so one data key seals at most **2^30** values (collision probability
  below 2^-37, well inside the 2^32 / 2^-32 limit of NIST SP 800-38D 8.3).
  Each process counts its seals per data key in memory and adds the count to
  `sealed_count` on every refresher tick (`UPDATE ... SET sealed_count =
  sealed_count + $n WHERE id = $dek`; a crash loses at most one tick, i.e.
  under-counts by seconds of traffic, which the 4x margin absorbs). When the
  refresher sees the active data key at `sealed_count >= 2^30`, it performs
  `rotate-data-key` step 1 itself (new active data key, the old one retired,
  under lock `(8400,2)`, logged 9.4); re-sealing old values is not needed
  for the nonce bound and is left to an operator `--retire` run. Expected
  load for the documented deployment (Google/Microsoft/Zoom renewals about
  hourly, two to three columns each, plus user saves) is in the order of
  10^2 seals per user per day: about 2^26 a year for 1 000 users, so the
  automatic step is a backstop, not a routine. `status` prints
  `sealed_count` and flags `rotate due` for an active data key older than
  365 days or past 2^29 seals (advisory).

### 4.2 Wrap format

`wrapped` = `nonce (12) || AES-256-GCM_KEK(data key, 32 bytes) || tag (16)`
= 60 bytes, KEK = the 32 bytes of the key named by `kek_id`. Wrap AAD:

```
"paceday/dek" 0x00  0x01  u32be(id)  lp16(kek_id)
```

So a wrapped key copied to another id, or relabelled with another `kek_id`,
fails to unwrap. Data keys are 32 bytes from `crypto/rand`.

### 4.3 Locks (AC-21b, AC-27, P-6)

Postgres advisory locks, two-int form, class `8400` (unused today: no
advisory lock exists in `backend/`):

| Lock | Mode | Held by | Purpose |
|---|---|---|---|
| `(8400, 1)` **maintenance** | session, `pg_try_advisory_lock`, on one dedicated `*sql.Conn` for the whole run | backend automatic conversion; `convert`, `rotate-data-key`, `decrypt-all`, `reset-lost-key` | at most one bulk pass at a time |
| `(8400, 2)` **key setup** | transaction, `pg_advisory_xact_lock` (blocking) | every key load (create first data key, unwrap, re-wrap under the active KEK), `rotate-data-key` step 1 | one process creates the first data key; re-wrap is atomic |

Every process names itself in `application_name`: the backend's pool uses
`paceday-backend`; the CLI sets `paceday-operator encryption <subcommand>`
(or `paceday-operator <subcommand>` for PAC-71's). A process refused by the
maintenance lock reads the holder from
`pg_locks JOIN pg_stat_activity` (`locktype='advisory' AND classid=8400 AND
objid=1 AND granted`) and names its `application_name`, `pid` and
`backend_start`.

**Backend presence (AC-21c).** The backend upserts its heartbeat row at start
and every 30 s, and deletes it on graceful shutdown. `decrypt-all` and
`reset-lost-key` take the maintenance lock **first**, then refuse (exit 6)
if any heartbeat has `seen_at > now() - interval '90 seconds'`. The backend
at start writes its heartbeat **first**, then refuses to start if the
maintenance lock is held by an `application_name` of
`paceday-operator encryption decrypt-all` or `… reset-lost-key`. Write-then-
check on both sides means at least one of two racing processes sees the
other. Heartbeats older than 1 day are deleted by any key load. A crashed
backend therefore blocks these two commands for at most 90 s.

**Seal lease (K-4).** A heartbeat older than 90 s does not prove the process
is gone: a backend whose database link stalled, or whose container was
paused, resumes with its keyring and would seal under a data key that
`decrypt-all` or `reset-lost-key` has deleted. So the heartbeat is also a
lease: `Seal` refuses (the write fails with the operation's documented 500;
`Open` is unaffected) while the last heartbeat upsert that **succeeded** was
**sent** more than 60 s ago (process monotonic clock; the row's `seen_at` is
the transaction's `now()`, which is at or after the send, so the lease always
ends before the 90 s window does). And a refresher tick that finds its
active data key missing from the table, or the maintenance lock held by
`decrypt-all`/`reset-lost-key`, stops sealing and begins the process's
graceful shutdown (exit 1 after the drain, logged 9.4) instead of carrying on
with keys the database no longer has.

**Amended by PAC-116 (2026-11-14; spec `docs/specs/PAC-116.md` revision 2,
contract `contracts/features/PAC-116.md` §2-§4; owner decision "stop
traffic", which reverses PAC-116's original "PAC-84 untouched").** The
maintenance lock `(8400, 1)` has one more kind of holder: a **migrator**.
This is any process applying pending migrations: the backend at start,
`paceday-operator encryption <sub>`, or PAC-71's subcommands. It holds the
lock on a dedicated connection whose `application_name` starts with
`paceday-migrate: `. Under the lock it refuses ("the app is serving") when
any heartbeat is live (90 s), **or** any other client session is connected to
the database. Only when neither is found does it apply the migrations, and
then it releases the lock. The backend at start writes its heartbeat and then
refuses to start on a migrating holder, as for `decrypt-all` and
`reset-lost-key`, deleting its heartbeat first. A refresher tick that finds a
migrating holder begins the graceful shutdown, as for those two commands. It
is the same write-then-check on both sides. With nothing pending, no lock is
taken and nothing changes. The text above is kept as PAC-84 approved it.

## 5. Environment and start order

### 5.1 Grammar

```
PACEDAY_ENCRYPTION_KEY          = entry
PACEDAY_ENCRYPTION_KEY_PREVIOUS = "" | entry *( *SP "," *SP entry )      ; at most 8 entries
entry  = key-id ":" key
key-id = [A-Za-z0-9] 0*31[A-Za-z0-9._-]
key    = standard base64 (RFC 4648 §4, "+/", padding required) decoding to exactly 32 bytes
```

- Leading/trailing whitespace of each variable is trimmed; an all-whitespace
  `_PREVIOUS` is "none". Base64url, unpadded or embedded whitespace is
  malformed (one encoding, the one `openssl rand -base64 32` prints).
- Refused as malformed: active missing or empty (reported as **missing**, not
  malformed); an entry without `:`; bad id; bad base64; not 32 bytes; more
  than 8 previous; an id repeated across active and previous; two entries with
  identical key bytes under different ids.
- Error text names the variable, the entry position (`entry 2 of
  PACEDAY_ENCRYPTION_KEY_PREVIOUS`) and the reason; it echoes the key id only
  when the entry parsed up to a valid id, and **never** echoes any other
  input byte (a malformed entry may be the key itself).
- Generation, as documented in `.env.example` and the runbook:
  `echo "PACEDAY_ENCRYPTION_KEY=$(date -u +%Y%m%d):$(openssl rand -base64 32)" > encryption.env && chmod 600 encryption.env`.

### 5.2 Where the key comes from (D-9)

- Production and development compose: the backend service declares
  `env_file: [{ path: ./encryption.env, required: false }]` (the `required`
  attribute needs **Docker Compose 2.24.0 or later**; `docs/runbook.md:22`
  says 2.20 today and is raised to 2.24.0 by this work, K-7; an older 2.2x
  rejects the long syntax before anything runs)
  and **neither** compose file names `PACEDAY_ENCRYPTION_KEY` or `_PREVIOUS`
  in `environment:` (an `environment:` entry, even `${VAR:-}`, would override
  the file with an empty value). `required: false` is deliberate: a missing
  file reaches the backend, which refuses with its own message (AC-8, AC-28)
  instead of a compose error that names no runbook section.
- `encryption.env` sits beside `.env`, mode 600, is git-ignored, and is never
  the file passed to `backup.sh --env-file` (section 10 refuses it).
- `paceday-operator` reads the same two variables from its environment.
  The image ships it as `/paceday-operator` (PAC-71's `backend/Dockerfile`
  change builds and copies it; if PAC-84 merges first it makes the same
  Dockerfile change, O-1 resolved). Invocation (K-13): subcommands that need
  the backend **stopped** (`decrypt-all`, `reset-lost-key`,
  `downgrade-schema`) run as `docker compose -f docker-compose.prod.yml run
  --rm --no-deps backend /paceday-operator encryption <subcommand> …` with
  postgres up (a `run` of the service gets the service's `env_file`;
  compose's own `--env-file` is the interpolation file and is **not** how the
  key arrives). The others may also use PAC-71's `docker compose … exec
  backend /paceday-operator …` against the running container.

### 5.3 Start order (`backend/main.go`, AC-8, AC-9, D-5)

1. `initSentry()`, existing env decisions (calendar, IdPs, LLM, cookies,
   rate limits).
2. **`config.DecideEncryptionKeys(os.Getenv)`** parses section 5.1. Missing
   or malformed -> `log.Fatalf("refusing to start: …")` **before the database
   is opened** (so the fresh-database case of AC-8 needs no schema).
3. `DATABASE_URL` check, `storage.Open` (migrations up), pool caps.
4. **Key load** (`storage.LoadKeyring`, section 6.2): heartbeat written;
   offline-operation check (section 4.3); under lock `(8400,2)`: create the
   first data key if none is active, unwrap **every** data key, re-wrap the
   ones wrapped by a previous KEK under the active KEK (D-6), all in one
   transaction. Any failure -> `refusing to start: …` (section 9.1).
   `secretbox.Install(keyring)`.
5. Everything that exists today after the pool (OAuth configs, engines,
   crons at `main.go:166-244`, `JWT_SECRET` check at `:248`, router).
   Moving the `JWT_SECRET` check is not part of this work.
6. Listener opens (`ListenAndServe`).
7. After the listener is open: the **keyring refresher** (every 30 s: reload
   data keys, unwrap only ids not yet held, `Install` the new keyring with
   the switched active id, flush `sealed_count`, **then** upsert the
   heartbeat with `active_dek`; the order matters: a heartbeat never reports
   an id the process is not yet sealing with) and the **automatic
   conversion** (section 8.3), both as `lifecycle` tracked units so the
   drain stops them between batches. A tick that cannot unwrap a new data
   key (its wrapping key is not configured in this process) keeps the
   current keyring, does not advance `active_dek`, and logs section 9.1's
   wrong/unknown-key text with the prefix `encryption: ` instead of
   `refusing to start: `; the process keeps serving (values under that key
   answer per AC-16 with reason `unknown_data_key`). A tick also applies the
   lease and shutdown rules of section 4.3 and the automatic step of 4.1.
   Multi-replica note: every start re-wraps under **its own** active key the
   data keys wrapped by a key in **its own** previous list; replicas started
   with swapped active/previous keys re-wrap back and forth, which is
   harmless (both can unwrap) but visible in `status`; the runbook says to
   roll a key change out to every replica with the same two variables.

The KEK is used only in step 4 and by the refresher for a data key id it has
not seen (created by `rotate-data-key` or by another process's first-key
creation). AC-26's counter equals the number of data keys over a run in which
no data key is created.

**Amended by PAC-116 (2026-11-14; contract `contracts/features/PAC-116.md`
§4.1).** Step 3 reads: `DATABASE_URL` check, then the **migrate-when-stopped
procedure** with caller `paceday-backend`, which applies pending migrations
only when the app is not serving (PAC-116 §3), before the serving pool is
opened. A refusal is `log.Fatalf("refusing to start: …")` before listening.
Then the pool is opened without migrating, and the pool caps are applied.
Step 4's offline-operation check also refuses on a migrating holder of
`(8400, 1)` (§4.3 amendment). The text above is kept as PAC-84 approved it.

## 6. Go package `backend/internal/secretbox`

Name chosen to match the existing `backend/internal/{budget,lifecycle,
outbound,peritem}` single-noun packages. It holds crypto, envelope, binding
and the in-memory keyring; it **does not** import `storage` and runs no SQL
of the inventory tables. `storage` imports it.

### 6.1 Interface (signatures are binding; unexported details are not)

```go
package secretbox

const Marker = "pdenc:"
const FormatV1 = 1

// Kind of a stored value, decided from its bytes only (section 3.1).
type Kind int
const ( KindEmpty Kind = iota; KindPlain; KindSealed )
func Classify(stored string) Kind
func SealedDEK(stored string) (id int32, ok bool)       // ok=false unless KindSealed and parseable

// Binding = where a value lives (section 3.2/3.3).
type Part struct{ Name, Value string }
type Binding struct {
    Table, Column string
    Row           []Part                               // 1 or 2 parts, order of 3.3
}
func (b Binding) AAD() []byte
func (b Binding) RowString() string                   // "user_id=…,calendar_id=…" for logs

// Keys from the environment (section 5.1). The key bytes are unexported and
// never formatted: KEK implements fmt.Formatter / String as its ID only.
type KEK struct{ ID string /* + unexported [32]byte */ }
type KeyConfig struct{ Active KEK; Previous []KEK }
func ParseKeyConfig(active, previous string) (KeyConfig, error)   // ErrKeyMissing / ErrKeyMalformed
func (c KeyConfig) Lookup(id string) (KEK, bool)

// A stored data key as read from encryption_data_keys.
type WrappedDEK struct{ ID int32; State string; KEKID string; Wrapped []byte }
func NewDEK(id int32, kek KEK) (WrappedDEK, error)    // random 32 bytes, wrapped (section 4.2)
func Rewrap(w WrappedDEK, cfg KeyConfig) (WrappedDEK, error)       // under cfg.Active

// Keyring: immutable snapshot of unwrapped data keys; safe for concurrent use.
type Keyring struct{ /* unexported */ }
func NewKeyring(cfg KeyConfig, deks []WrappedDEK) (*Keyring, error) // ErrWrongKey / ErrUnknownKEK / ErrNoActiveDEK
func (k *Keyring) With(cfg KeyConfig, more []WrappedDEK) (*Keyring, error) // refresher: unwraps only new ids
func (k *Keyring) ActiveDEK() int32
func (k *Keyring) Seal(b Binding, plaintext string) (string, error) // ""->"", never returns plaintext
func (k *Keyring) SealWith(dek int32, b Binding, plaintext string) (string, error) // rotation
func (k *Keyring) Open(b Binding, stored string) (string, error)    // empty->"", plain->as is, sealed->decrypt
func (k *Keyring) Unwraps() int64                                   // AC-26 counter

// Process-wide keyring, installed at start (like calendar.Configure) and
// replaced by the refresher. The package-level Seal/Open read Current() at
// call time: no caller holds a *Keyring across a request or cron item, so a
// seal started after a heartbeat reports a new active id uses it (K-5).
func Install(k *Keyring)
func Current() *Keyring                               // nil before Install
func Seal(b Binding, plaintext string) (string, error) // ErrNoKeyring if none installed
func Open(b Binding, stored string) (string, error)    // empty/plain work without a keyring; sealed -> ErrNoKeyring

// Errors. None carries input bytes, ciphertext or plaintext.
var (
    ErrDecrypt     = errors.New("stored secret cannot be decrypted")
    ErrNoKeyring   = errors.New("encryption keyring not installed")
    ErrKeyMissing, ErrKeyMalformed, ErrWrongKey, ErrUnknownKEK, ErrNoActiveDEK error
)
type Reason string // "authentication_failed" | "unknown_data_key" | "malformed" | "unsupported_version" | "no_keyring"
type DecryptError struct {
    Table, Column, Row string; DEK int32; KEK string; Reason Reason
}
func (e *DecryptError) Error() string        // section 9.2 text, identifiers only
func (e *DecryptError) Is(target error) bool // true for ErrDecrypt
```

`secretbox/secretboxtest`: `Install(t testing.TB) *secretbox.Keyring`
installs a keyring with a random KEK and one data key (restored on cleanup),
and `Key(t) string` returns a random valid `PACEDAY_ENCRYPTION_KEY` value for
tests that boot `run()` with `t.Setenv`.

**KMS seam (Q-1 "KMS later").** The KEK is touched only by `NewDEK`,
`Rewrap`, `NewKeyring` and `Keyring.With`. A KMS replaces those four with
remote wrap/unwrap; no storage code changes.

### 6.2 Storage rules (binding on stage 4; function names are the plan's)

- Every write of an inventory column seals through `secretbox.Seal` with the
  section 3.3 binding. `''` and `NULL` are written as is. With no keyring
  installed a write **fails**; it never falls back to plaintext.
- Every read of an inventory column opens through `secretbox.Open`.
- **One undecryptable secret never fails a multi-secret read, a save, or a
  list** (AC-16c, P-4). Read functions return the row with each secret in a
  form that defers or records its own failure (lazy per-field decryption, or
  a per-field error); the plan picks the type. **A read function never
  returns a decryption failure as its own error** (K-2: callers such as
  `GetSSOProviderByDomain`, `GetWorkspaceConnection`, `GetPersonalCalendar`
  map any error to 404/400 today, section 1). Every save that keeps an
  existing secret, not only the settings save, writes it **byte-identical to
  what it read** (or omits the column): settings (S2, S3, S6, S8), the SSO
  kept client secret (`sso_providers.go:199-203`, same row id so the stored
  text stays valid), the org AI kept key (S9), the personal calendar
  `url = COALESCE($4, url)` update; it never re-seals or blanks a value it
  could not open (K-10).
- Presence checks keep comparing the stored text with `''` (in Go and in
  SQL); sealed text is non-empty iff the plaintext was (AC-7, AC-16b).
- `removeCredential` and its callers (`handlers_credentials.go:125-147`)
  decrypt the removed value for the Zoom revocation; a decryption failure
  reports `unconfirmed` and the removal still succeeds (AC-16c).
- A decryption failure is wrapped so the caller can tell it apart from a
  storage failure with `errors.Is(err, secretbox.ErrDecrypt)`; in
  `engine.LoadCalendarToken` it is still under `ErrCredentialStore` (500) for
  act paths, and `engine/booking.go` treats it as **unreadable host** (busy,
  D-6), not as the D-7 listing failure.
- The PAC-67 classifier never sees a decryption failure as `invalid_grant`:
  no provider call is made with an undecryptable value.

## 7. Migration

### 7.1 Up: `NNN_encryption_at_rest.up.sql`

```sql
-- operator_audit: created here if PAC-71 has not merged (same definition as
-- PAC-71's 033), then its action CHECK is widened. Works in either order.
CREATE TABLE IF NOT EXISTS operator_audit ( …PAC-71 033 lines 61-74 verbatim… );
ALTER TABLE operator_audit DROP CONSTRAINT IF EXISTS operator_audit_action_check;
ALTER TABLE operator_audit ADD CONSTRAINT operator_audit_action_check CHECK (action IN (
    'free_address', 'reassign', 'advance_all',
    'encryption_convert', 'encryption_rotate_data_key', 'encryption_decrypt_all',
    'encryption_reset_lost_key', 'encryption_downgrade_schema'));
-- section 4.1: sequence, encryption_data_keys, one-active index, heartbeats.
```

`operator_audit_action_check` is the name Postgres gives PAC-71's inline
column CHECK. **Copy drift (K-9):** `IF NOT EXISTS` hides a difference between
PAC-84's copy and PAC-71's text. Stage 4 adds a storage test that pins
`operator_audit`'s columns, types, defaults and nullability to PAC-71's
definition at `impl/pac-71-identity-binding-backend` `033` lines 61-74, and
whichever of PAC-71 / PAC-84 merges second re-runs it against the first's
text; a difference is fixed in the second one's own migration with `ALTER
TABLE`, never by editing the merged one. Any later migration that changes
the action list restates **all** eight actions (a comment in this migration
says so). No inventory column changes type, default or nullability (D-3).
The up step encrypts nothing (SQL has no key); the conversion does.

### 7.2 Down: `NNN_encryption_at_rest.down.sql`

1. A `DO` block raises
   `PAC-84 down refused: encrypted values or data keys remain; run "paceday-operator encryption decrypt-all" first (docs/runbook.md, "Encryption key - downgrade")`
   if `encryption_data_keys` has any row or any of the 18 columns has a value
   with `left(col, 6) = 'pdenc:'`. The file runs as one simple-protocol
   statement batch (one implicit transaction), so the refusal changes no
   schema. **But golang-migrate marks the version dirty before it runs a
   step** (`migrate/v4@v4.19.1/migrate.go:738-751`: `SetVersion(NNN-1, true)`,
   run, `SetVersion(NNN-1, false)`), so a refused step leaves
   `schema_migrations` at `(NNN-1, dirty)` over an unchanged NNN schema, and
   the next `storage.Open` (backend or CLI) fails with `ErrDirty` (K-1).
   Therefore `downgrade-schema` (section 8.2) performs the same check **in
   Go first**, under the maintenance lock with no live backend, and refuses
   with exit 1 and the same text without touching `schema_migrations`; if the
   down step still fails, it calls `Force(NNN)` (the schema is unchanged) and
   exits 1. The `DO` block stays as the backstop for any other runner.
2. Drops `encryption_backend_heartbeats`, `encryption_data_keys`,
   `encryption_data_keys_id_seq`.
3. **Keeps** `operator_audit` and its widened CHECK (spec §7; narrowing it
   would fail on the `encryption_decrypt_all` row the downgrade just wrote).

The down step is run by `paceday-operator encryption downgrade-schema`
(section 8): nothing else in the deployment runs a down migration.

### 7.3 Number and order with PAC-71 (P-2)

`NNN` is the **next free number on `main` at merge time**. Today that is
`033` if PAC-84 merges first and `034` if PAC-71 (which owns `033`) merges
first, as preferred. Whichever merges second renames its own pair (up and down
together) and nothing else. The 7.1 text is order-independent:

- **PAC-71 first:** its `033` created `operator_audit` with the 3-action CHECK;
  PAC-84's `034` finds the table, replaces the CHECK with the 8-action one.
  PAC-84 adds `encryption` to the existing `cmd/paceday-operator`.
- **PAC-84 first:** its `033` creates `operator_audit` with the 8-action CHECK
  and creates `cmd/paceday-operator` with only the `encryption` command.
  PAC-71 renumbers to `034`; its `CREATE TABLE IF NOT EXISTS operator_audit`
  is a no-op and the CHECK already admits its three actions. PAC-71 must not
  re-add a narrower CHECK.

## 8. Operator command `paceday-operator encryption`

### 8.1 Common

- Form: `paceday-operator encryption <subcommand> [flags]`. Flags use Go's
  `flag` package as PAC-71's do (`-operator` and `--operator` are the same).
- `DATABASE_URL` required (exit 2 as today); migrations are applied first, as
  PAC-71's `main` does (`storage.Open`).
- Mutating subcommands require non-empty `--operator` and `--reason`, then
  take the maintenance lock, then do their work, and write **exactly one**
  `operator_audit` row when they end, completed or failed (`account_id` and
  identity columns NULL; `details` as 8.4). Refusals with exit 2, 4, 5, 6
  change nothing and write no audit row.
- Output: human-readable lines on stdout; errors on stderr. Neither ever
  contains a value, ciphertext or key byte; key ids and data-key ids are
  printed.

**Exit codes** (the existing PAC-71 subcommands keep theirs):

| Code | Meaning |
|---|---|
| 0 | done (including "nothing to do") |
| 1 | the run failed or ended incomplete (database error, values that could not be processed, rotation wait timed out); the audit row says `failed`/`partial` and a re-run resumes |
| 2 | usage: unknown subcommand or flag, missing `--operator`/`--reason`/`--retire`/`--confirm-lost-key`, missing `DATABASE_URL` |
| 3 | `status --check` only: at least one plain value remains |
| 4 | key refused: active key missing/malformed, a configured key does not open a data key, a data key wrapped by an unknown id (section 9.1 text); for `reset-lost-key`: a configured key **does** open a stored data key |
| 5 | maintenance lock held by another process (named) |
| 6 | a backend is running (heartbeat within 90 s; `decrypt-all`, `reset-lost-key`) |

**Amended by PAC-116 (2026-11-14; contract `contracts/features/PAC-116.md`
§3, §5, §6).** "Migrations are applied first" now goes only through PAC-116's
migrate-when-stopped procedure, for every subcommand that reaches it,
`status` included. With pending migrations and the app serving (a live
heartbeat or any other client session), the subcommand applies nothing and
exits **6**, whose meaning widens to "a backend is running / the app is
serving". It lists the heartbeats and sessions it saw. With the maintenance
lock held by another process when a migration is pending, it exits **5**.
Neither writes an audit row. There is no flag that overrides the refusal.
With nothing pending, behaviour is unchanged. The text and table above are
kept as PAC-84 approved them.

### 8.2 Subcommands

| Subcommand | Flags | Key needed | Lock | Does | Audit action |
|---|---|---|---|---|---|
| `status` | `--check` | none | none | Per inventory column: `empty`, `plain`, `encrypted` counts and encrypted per data key; per data key: id, state, wrapping key id, created (age in days), wrapped_at, retired_at, values. Live heartbeats (instance, host, active_dek, seen). Read-only. | none |
| `convert` | `--operator --reason [--batch 100]` | active (+ previous to unwrap); exit 4 unless the active key id equals the `kek_id` of the active data key (K-6) | maintenance | Encrypts every plain value (8.3). | `encryption_convert` |
| `rotate-data-key` | `--operator --reason --retire <dek id> [--batch 100] [--wait 5m]` | active (+ previous); exit 4 unless the active key id equals the `kek_id` of the active data key, so no data key is created that the running backends cannot unwrap (K-6) | maintenance; key setup for step 1 | (1) if `<dek id>` is active: in one transaction create a new active data key and mark `<dek id>` retired; (2) wait until every live heartbeat reports the new `active_dek` (none live: no wait), else exit 1 after `--wait` (a re-run resumes); (3) re-seal every value under `<dek id>` with the active key in batches (8.3 compare). Re-run with the same `--retire` after completion: 0 changes. | `encryption_rotate_data_key` |
| `decrypt-all` | `--operator --reason [--empty-undecryptable]` | keys that open every data key | maintenance + no live backend | Writes every sealed value back as plaintext (8.3 compare; the only path allowed to send plaintext, AC-19c). Values that fail to decrypt are listed (table, column, row, dek, reason) and left as they are -> exit 1, data keys kept; with `--empty-undecryptable` they get the AC-23 per-secret removal instead. When no sealed value remains, deletes every data key. | `encryption_decrypt_all` |
| `reset-lost-key` | `--operator --reason --confirm-lost-key` | the **new** active key only | maintenance + no live backend | AC-23 exactly: every sealed value gets its owner's-removal end state, no provider call; plain values untouched; every data key deleted; one new active data key created under the new key. Exit 4 if any configured key opens a stored data key ("the key is not lost"). | `encryption_reset_lost_key` |
| `downgrade-schema` | `--operator --reason` | none | maintenance + no live backend | Runs this work's down step (7.2) via the embedded migrations, only if the database's current version is this work's `NNN` (a later migration applied -> exit 1, "roll back the later release first"). | `encryption_downgrade_schema` |

`downgrade-schema` is factory-assumed (not in D-8): AC-22 requires the runbook
to name the command that runs the down step, and nothing can today (the
backend migrates up only, `storage/db.go:59`). Its audit row is written after
the down step, into the kept `operator_audit`.

**Amended by PAC-116 (2026-11-14).** In every row, the implicit "apply
pending migrations" step before the lock column follows the §8.1 amendment.
`status` (lock "none") takes `(8400, 1)` only for that step, and only when a
migration is pending. `downgrade-schema` is not routed through it: it applies
no pending up migration and keeps its own heartbeat check. The table above is
kept as PAC-84 approved it.

### 8.3 Batch passes (conversion, rotation, decrypt-all)

- Per inventory column, batches of `--batch` rows (default 100, 1..1000), one
  short transaction each: `SELECT <row identity>, <column>, xmin::text FROM t
  WHERE <selector> ORDER BY <identity> LIMIT n`, then per row
  `UPDATE t SET <column> = $sealed WHERE <identity> AND xmin::text = $xmin`.
  Selector: convert = plain (non-empty, `left(col,6) <> 'pdenc:'`); rotate =
  `split_part(col, ':', 3) = '<dek id>'` among sealed; decrypt-all = sealed.
- The `SELECT` and its `UPDATE`s run in **one** `READ COMMITTED`
  transaction. Why `xmin` is a sound compare-and-set (K-17): every `UPDATE`
  of the row, HOT or not, writes a new tuple version whose `xmin` is the
  writer's transaction id; a concurrent committed writer makes the `UPDATE`
  re-check its `WHERE` on the newest version (EvalPlanQual), whose `xmin`
  differs; freezing (Postgres >= 9.4; the images are `postgres:16`) keeps the
  raw `xmin` and creates no version; a false match would need the same
  32-bit xid to come round again (2^31 transactions) inside one short
  transaction. A concurrent writer that writes back text it read before the
  pass (the byte-identical rule of 6.2) may restore a plain value or one
  under a retired key; the next sweep or run picks it up, nothing is lost.
- The compare is on the row version, not on the value: **no plaintext is sent
  as a parameter or literal** by convert or rotate (AC-19c). A row changed in
  between (any column) is skipped this batch and picked up by the next one;
  the pass loops until a full sweep updates nothing (bounded: at most 3
  sweeps, then it ends `partial` with the count left, exit 1).
- Only the secret column is in the `SET` (AC-12: `updated_at`,
  `connected_at`, `configuration_version` untouched; no triggers exist on
  these tables).
- Sealing with a data key happens only after that key's row is committed
  (AC-21b: no value under an unrecorded key).
- **Backend automatic conversion** (P-3 default: both): after the listener
  opens, try the maintenance lock; if held, retry every 60 s until it gets it
  or the process stops; run convert's pass with batch 100 and a 50 ms pause
  between batches; once per process start. Not audited (not an operator
  action); logged (9.4).

### 8.4 Audit `details`

```json
{ "outcome": "completed" | "partial" | "failed",
  "keyIds": { "active": "<id>", "previous": ["<id>", …] },
  "dataKeys": { "created": [n], "retired": [n], "removed": [n] },
  "counts": { "<table>.<column>": { "encrypted"|"reencrypted"|"decrypted"|"emptied"|"deleted"|"disabled"|"skipped": n } },
  "lossesCleared": n, "startedAt": "<RFC 3339>", "finishedAt": "<RFC 3339>" }
```

Only the keys that apply to the action appear. Never a value.

## 9. Errors and logs

### 9.1 Start refusals (backend: `log.Fatalf`, exit 1; CLI: stderr, exit 4)

Every line starts `refusing to start: ` (CLI: `encryption: `) and ends
`(docs/runbook.md, "Encryption key")`:

- `PACEDAY_ENCRYPTION_KEY is not set; …`
- `PACEDAY_ENCRYPTION_KEY is malformed: <reason>` /
  `entry <i> of PACEDAY_ENCRYPTION_KEY_PREVIOUS is malformed: <reason>`;
  reasons: `expected <key id>:<base64 of 32 bytes>`, `invalid key id`,
  `invalid base64`, `decodes to <n> bytes, need 32`, `key id "<id>" appears twice`,
  `keys "<a>" and "<b>" are the same key`, `more than 8 previous keys`.
- `key "<id>" (PACEDAY_ENCRYPTION_KEY[_PREVIOUS]) does not open data key <n> (wrapped by key "<id>")` (wrong key, AC-9)
- `data key <n> is wrapped by key "<id>", which is neither PACEDAY_ENCRYPTION_KEY ("<id>") nor in PACEDAY_ENCRYPTION_KEY_PREVIOUS (<ids or "none">)` (AC-10)
- `paceday-operator encryption <decrypt-all|reset-lost-key> is running (pid <p>); start the backend after it ends`
- `PACEDAY_ENCRYPTION_KEY[_PREVIOUS] is the committed e2e test key "<id>"; generate a real key` (K-12; unless `PACEDAY_ENCRYPTION_ALLOW_TEST_KEY=true`, section 11)

No refusal names `JWT_SECRET` or any other variable (AC-8b keeps each case
naming only its own).

### 9.2 Decryption failure while serving (AC-16..AC-19)

One line per failure, via `log.Printf`, and the same text as the error-reporter
message (no `extra` fields carrying values):

```
encryption: cannot decrypt <table>.<column> row=<RowString> dek=<n|-> kek=<id|-> user=<uuid|-> reason=<reason>
```

`user` is the acting or owning user when the caller knows it (org/SSO rows:
`-`). The same line is written on every path of AC-16..AC-18 and by the
crons (AC-17), once per failing secret per request or per cron item.

### 9.3 HTTP mapping (no contract change, section 1)

| Path kind | Answer |
|---|---|
| act (needs the secret) | existing 500 `{"error":"internal error"}` via `writeServerError` |
| status / presence | existing mapping (not connected / `*Set` true) |
| best-effort (briefs, free/busy, booking coverage) | skip, as for any unusable credential |
| public booking host | unreadable -> busy (D-6); never D-7's 500 |
| lists and fetches (personal calendars) | the row is listed; an undecryptable `url` is **omitted** (note below) |
| disconnect | 200, Zoom revocation `unconfirmed` |

Note on the list row (O-5 decided, K-11): S5 is returned to its owner today;
a row whose `url` cannot be decrypted is listed (and fetched by
`listPersonalCalendars`, the preview and the mark-synced answer) with `url`
**omitted** and everything else as stored. `""` was the author's choice, but
`personalCalendarDTO.URL` is `json:"url,omitempty"`
(`handlers_personal.go:37`) and the bundle's `PersonalCalendar.url` is
"Omitted when empty", so `""` is not even observable: omission is what the
existing shape already says for "no usable URL". Rejected: omitting the row
(hides it from the delete AC-16c(d) requires), a 500 for the list (AC-16c
forbids), the ciphertext (AC-19). AC-19b's "unchanged in value" holds for
every value that decrypts. The bundle's description is not amended (no shape
change; a description-only bundle revision would force a web regeneration
for nothing): this note is the normative text. The plan adds a test.

### 9.4 Informational lines

- start: `encryption: key "<id>" active; <n> data keys (active <m>); <r> re-wrapped from previous keys`
- previous key no longer used: `encryption: previous key "<id>" wraps no data key; remove it once the backup retention (14 days) has passed (docs/runbook.md, "Encryption key")`
- automatic step (4.1): `encryption: data key <n> reached <count> seals; data key <m> is now active`
- lease/shutdown (4.3): `encryption: heartbeat not written for <d>; sealing paused` / `encryption: data key <n> no longer exists (or <subcommand> holds the maintenance lock); shutting down`
- conversion: `encryption: conversion started` / `encryption: conversion done: <n> values encrypted in <d>` / `encryption: conversion waiting: maintenance lock held by <application_name> (pid <p>)`

## 10. `scripts/ops/backup.sh` refusal (AC-4)

- Checked right after argument validation (`[ -f "$env_file" ]`), **before**
  container lookup, directory creation or `pg_dump`: no file and no directory
  is written.
- A line triggers when (CRLF tolerated) it matches
  `^[[:space:]]*(export[[:space:]]+)?(PACEDAY_ENCRYPTION_KEY|PACEDAY_ENCRYPTION_KEY_PREVIOUS)[[:space:]]*=(.*)$`
  and the captured value, whitespace-trimmed and with one pair of matching
  surrounding `'` or `"` removed, is non-empty. Lines whose first non-space
  character is `#` never trigger.
- Message (stderr), exit **2**:
  `backup: <env file> line <n> sets <VAR>; the encryption key must live in encryption.env, outside the backup set (docs/runbook.md, "Encryption key"). Nothing was written.`
  The value is never printed.
- The header comment's "a dump contains every user's stored provider
  credentials in plain columns" is replaced by what is true after the
  conversion (and what a pre-conversion dump still holds).

## 11. Key sources for every boot (AC-8b, AC-28)

| Boot | Key source |
|---|---|
| `docker-compose.yml` (dev) | `env_file: ./encryption.env` (`required: false`); no committed key. Without the file the backend refuses like production. README and `.env.example` give the generation command. |
| `docker-compose.prod.yml` | same `env_file`; `environment:` does not name the key variables. |
| `e2e/docker-compose.e2e.yml` | `PACEDAY_ENCRYPTION_KEY: ${E2E_ENCRYPTION_KEY:-e2e-test:cGFjZWRheS1lMmUtdGVzdC1rZXktTk9ULVNFQ1JFVCE=}` (32 ASCII bytes `paceday-e2e-test-key-NOT-SECRET!`), same pattern as `E2E_JWT_SECRET`, plus `PACEDAY_ENCRYPTION_ALLOW_TEST_KEY: "true"` (K-12). |
| `cd backend && go run .` (README) | the developer exports `encryption.env` first (`set -a; . ../encryption.env; set +a`); README says so next to the generation command (K-15). |
| e2e rotation / wrong-key cases (AC-2, AC-9, AC-11) | the e2e key above plus a second committed constant in `e2e/lib`: `e2e-test-2:cGFjZWRheS1lMmUtdGVzdC1rZXktMi1OT1RTRUNSRVQ=` (`paceday-e2e-test-key-2-NOTSECRET`). |
| `e2e/tests/startup.spec.ts` `BOOTABLE` | adds `PACEDAY_ENCRYPTION_KEY` = the e2e key and `PACEDAY_ENCRYPTION_ALLOW_TEST_KEY=true`; one new refusal case boots with the e2e key **without** the allow variable and expects the 9.1 test-key refusal. |
| production-reference boot (`:72-90`) | `productionReferenceEnvironment()` output plus `PACEDAY_ENCRYPTION_KEY` = the e2e key, added explicitly with a comment saying production gets it from `encryption.env`; the test also asserts, from the YAML text, that the prod backend service declares that `env_file` and that its `environment:` names neither key variable. |
| Go suite (`scripts/test-backend.sh`, testcontainers) | `secretboxtest.Install` / `secretboxtest.Key`: a random key per test process, generated in committed test code; the script itself needs no variable. |
| `scripts/ops/rehearse-backup-restore.sh` | `openssl rand -base64 32` per run into `$work/encryption.env`, outside the backup directory; `launch_backend` gains the key argument; a second random key for the negative control. |

The two committed e2e keys are test-only and appear only under `e2e/`
(and the `#:` line of `E2E_ENCRYPTION_KEY`). **O-3 decided (K-12):** the
backend and the CLI refuse to start (9.1, exit 4 for the CLI) when the
active key **or** any previous key has the key bytes of either committed e2e
key, unless `PACEDAY_ENCRYPTION_ALLOW_TEST_KEY` is exactly `true`. The
comparison is on the 32 bytes, so renaming the id does not get round it. Only
the e2e compose and `startup.spec.ts` set the variable; the
production-reference assertion of AC-8b also checks that
`docker-compose.prod.yml` does not name it. (No deployment-mode variable
exists in the backend to key this on, so the opt-in is explicit.)

## 12. `.env.example` and runbook text (AC-24, AC-25)

`.env.example` gains, with **no** `PACEDAY_ENCRYPTION_KEY=` value line:

```
# ── Encryption key (PAC-84) ── lives in encryption.env beside this file, NEVER here
#: PACEDAY_ENCRYPTION_KEY | required | default: none (the backend refuses to start) | <key id>:<base64 of 32 random bytes>; wraps the data keys that encrypt stored credentials. Put it in encryption.env (mode 600), not in .env: backup.sh refuses an env file holding it. Generate: echo "PACEDAY_ENCRYPTION_KEY=$(date -u +%Y%m%d):$(openssl rand -base64 32)" > encryption.env. Losing it loses every stored connection (docs/runbook.md, "Encryption key").
#: PACEDAY_ENCRYPTION_KEY_PREVIOUS | optional | default: empty | Comma-separated retired keys, same format, accepted to re-wrap data keys during a rotation; keep each at least 14 days after retiring it (backup retention). encryption.env only.
#: E2E_ENCRYPTION_KEY | test-only | default: e2e-test:cGFjZWRheS1lMmUtdGVzdC1rZXktTk9ULVNFQ1JFVCE= | PACEDAY_ENCRYPTION_KEY of the e2e stack (e2e/docker-compose.e2e.yml). Never use it anywhere else: the backend refuses it unless PACEDAY_ENCRYPTION_ALLOW_TEST_KEY=true.
#: PACEDAY_ENCRYPTION_ALLOW_TEST_KEY | test-only | default: empty | "true" lets the backend start with a committed e2e key. Set only by the e2e stack; never in a deployment.
```

The SECRETS paragraph (`.env.example:23-25`) says the encryption key is not
in this file and is kept apart from the backup set. Runbook section titles
referenced by messages: **"Encryption key"**, **"Encryption key - stop the
backend first"**, **"Encryption key - downgrade"** (the plan may nest them;
the strings in messages must match the headings).

## 13. Why the interface is shaped this way, and what was rejected

1. **One text column, prefixed envelope, instead of `BYTEA` columns or a side
   table.** Changing column types would touch every query and every
   nullable/default-empty presence check and make conversion a schema change;
   a side table of secrets keyed by (table, row) would break the PAC-67
   "update only while connected" SQL that tests the column itself. Rejected.
2. **`pgcrypto`** (spec D-1): rejected, the key would be a statement
   parameter in Postgres logs.
3. **Row identity by SSO domain** (the C-8 option `lower(domain)`): rejected
   for `id`, because Go and Postgres lower-casing can differ outside ASCII and
   the domain is rewritten in place; `id` needs the two-statement write, a
   cost paid once in one storage function.
4. **Deleting a data key after rotation**: rejected; keeping retired keys
   costs 60 bytes and removes the race with a backend that has not yet seen
   the rotation. With retired keys kept, `rotate-data-key` needs an explicit
   `--retire <id>` so that a second run is a no-op (AC-21) instead of another
   rotation.
5. **Compare-and-set on the old plaintext** (spec C-10): rejected for
   `xmin`, which sends nothing secret and also catches a concurrent write to
   another column (a skipped row costs one more batch, not correctness).
6. **`required: true` on the compose `env_file`**: rejected; compose would
   fail with its own text and the backend's runbook-naming refusal (AC-8)
   would never be seen.
7. **A backend connection held for life** to prove "a backend is running"
   (AC-21c): rejected; it costs a pooled connection and an idle pool may hold
   none. A heartbeat row with write-then-check on both sides closes the race
   without holding anything.
8. **Stripping the key in `backup.sh`** instead of refusing (D-9): rejected
   by the spec; kept.
9. **A JSON output mode for `status`**: not added; `--check` is the
   scripting interface the spec asks for.

## 14. Factory-assumed decisions (spec silent or open)

| # | Decision | Where |
|---|---|---|
| F-1 | Marker `pdenc:`, version `1`, decimal data-key id, base64url unpadded body | 3.1 |
| F-2 | AAD encoding with lp16 fields; data-key id not in AAD | 3.2 |
| F-3 | Row identity: SSO and personal calendars by `id`; others per 3.3 | 3.3 |
| F-4 | S5 = whole `url` column, every provider | 2 |
| F-5 | Retired data keys kept; `rotate-data-key --retire <id>` | 4.1, 8.2 |
| F-6 | Keyring refresher every 30 s (so rotation reaches running backends); heartbeat table with `active_dek` | 4.3, 5.3 |
| F-7 | Advisory lock class 8400; heartbeat presence with 90 s window | 4.3 |
| F-8 | Standard padded base64 only; at most 8 previous keys; duplicate bytes refused | 5.1 |
| F-9 | Key in `encryption.env` via compose `env_file`, `required: false` | 5.2, 11 |
| F-10 | Missing/malformed key refuses before the database is opened | 5.3 |
| F-11 | Package `backend/internal/secretbox`, process-wide `Install` like `calendar.Configure` | 6 |
| F-12 | Sixth subcommand `downgrade-schema` | 8.2 |
| F-13 | CLI exit codes 0-6 | 8.1 |
| F-14 | `decrypt-all --empty-undecryptable` | 8.2 |
| F-15 | (superseded by F-22) | 9.3 |
| F-16 | backup.sh refusal exits 2 (input refusal, like its other argument checks) | 10 |
| F-17 | Committed e2e keys; random keys for Go and the rehearsal | 11 |
| F-18 | One migration text for either merge order with PAC-71 | 7.3 |
| F-19 | Seal lease (60 s) and refresher shutdown on lost data key (K-4) | 4.3 |
| F-20 | 2^30 seals per data key, `sealed_count`, automatic rotation step 1 (K-8) | 4.1 |
| F-21 | Committed e2e keys refused unless `PACEDAY_ENCRYPTION_ALLOW_TEST_KEY=true` (K-12) | 9.1, 11 |
| F-22 | Undecryptable personal-calendar `url` omitted (replaces F-15's `""`, K-11) | 9.3 |

## 15. Open items for the challenger / plan

- **O-1** Resolved (K-13): PAC-71's `backend/Dockerfile` builds and copies
  `/paceday-operator`; PAC-84 makes the same change if it merges first;
  invocation in 5.2.
- **O-2** Resolved (K-8): 2^30 seals per data key, counted, automatic step 1
  at the bound (4.1).
- **O-3** Resolved (K-12): committed e2e keys refused unless
  `PACEDAY_ENCRYPTION_ALLOW_TEST_KEY=true` (11).
- **O-4** The env-file key is visible in `docker inspect` to whoever controls
  Docker on the host; accepted under Q-1 (KMS later), stated in the runbook.
- **O-5** Resolved (K-11): `url` omitted (9.3).

## Stage-4 amendments (review of #251, 2026-11-02)

Recorded by the implementer at the coordinator's request. None of them
changes the HTTP bundle.

- **3.1:** damaged-marker values are classified sealed (malformed); see
  the note in 3.1.
- **4.3 / 5.3 step 7 (review #2, K-4):** the refresher renews the seal
  lease from the time the tick *started*, so a stall inside the tick counts
  against the lease. Its heartbeat upsert writes only while the active data
  key still exists. When the upsert writes nothing, the process renews
  nothing and shuts down.
- **8.1 (review #3):** `paceday-operator encryption` never migrates a
  database whose schema is below this work's version, whether it was
  downgraded or not yet upgraded:
  - `status` reports this and exits 0, or 1 with `--check`;
  - every mutating subcommand refuses with exit 1 and writes an audit row
    when `operator_audit` exists.

  Otherwise migrations are applied first, as before.
- **8.1 / 8.4 (review #6):** every exit-1 end of a mutating subcommand
  writes exactly one audit row (`failed`). `reset-lost-key` (and
  `decrypt-all --empty-undecryptable`) record `lossesCleared`.
- **9.2 (review #4):** each distinct line is logged and reported once per
  hour per process, not on every storage read of the row.
- **Manifest:** `compose-test/` is added to `allowedPaths`, so the compose
  gate gives the backend its own random key (review #1, AC-28).

### Amendment with PAC-71 (merge into impl/pac-71-identity-binding-backend, owner decision 2026-11-12)

Recorded by the PAC-71 implementer at the merge of main (PAC-84 32ecacf)
into the PAC-71 branch. No HTTP bundle change.

- **7.2 / 7.3 / 8.2, downgrade with a later migration:** PAC-71's
  migration is renumbered `035_identity_binding` (7.3, "PAC-84 first").
  Because every `paceday-operator encryption` subcommand first migrates up
  to the newest embedded version, a release that carries 035 always has its
  database at 035, and `downgrade-schema` (which runs only from this work's
  version, 034) refuses: exit 1, `the database is at version 35, not 34:
  roll back the later release first`, nothing changed, `schema_migrations`
  clean, one `failed` audit row. **Owner decision (2026-11-12): this is
  accepted.** Once 035 ships, the way back to a release before encryption
  at rest is a restore of the pre-deploy backup (`docs/runbook.md`,
  "Encryption key - downgrade"); nothing in production runs PAC-71's down
  migration, and `downgrade-schema` is not extended to step through later
  migrations.
- **Tests:** the downgrade path at 034 is still proven unchanged
  (`TestPAC84_DecryptAllAndDowngrade`, `TestPAC84_OperatorDecryptDowngradeReset`,
  `TestPAC84_OperatorAfterDowngradeNeverMigratesUp`) on a database held at
  034 by the test-only `storage.SetMigrationCeilingForTest` (every
  `Open`/`Migrate` in the test process, the CLI's included, stops at 034 and
  never migrates down). `TestPAC84_DowngradeRefusedAbove034` covers the
  shipped schema (035): the refusal above.
- **7.1 (K-9):** `035_identity_binding`'s `CREATE TABLE IF NOT EXISTS
  operator_audit` (a no-op after 034) restates all eight actions in its
  CHECK; `TestPAC84_OperatorAuditPinned` passes against it.

## Challenge — 2026-11-02

**Verdict**: accept-with-changes (all changes applied in place above; no
bundle change, `contractHash` stays `a15d981e…ee472`, verified:
`sha256sum contracts/openapi/openapi.yaml` equals `origin/main`'s, and
`py -3 scripts/openapi_assemble.py --check` -> `OK: bundle is in sync (106 paths)`).
Code read at `origin/main` `953f96e` and `origin/impl/pac-71-identity-binding-backend` `559b6c1`.

### Requests wrongly permitted / divergences (each fixed above)

1. **K-1 (high) Refused down step leaves the database dirty.** golang-migrate
   v4.19.1 (`migrate.go:738-751`) writes `SetVersion(NNN-1, dirty=true)`
   before running a down body. With the 7.2 `DO` refusal, the schema is
   unchanged but `schema_migrations` reads `(NNN-1, dirty)`, so the next
   backend start fails `migrate up: Dirty database version NNN-1` and 7.2's
   "the refusal changes nothing" was false. Fix: `downgrade-schema` checks in
   Go first and `Force(NNN)` if the step still fails (7.2, AC-22).
2. **K-2 (high) Act paths that would not reach the documented 500.** Beyond
   SSO (`handlers_sso.go:57-61,122-125`) and AI: `handlers_conferencing.go:52-63`
   maps a Zoom decode failure / any `LoadMicrosoftToken` error to 400
   refusals; `handlers_daily_recap.go:159-162` maps any
   `GetWorkspaceConnection` error to 400 "Slack not connected";
   `handlers_personal.go:147-155,207-210` map any `GetPersonalCalendar`
   error to 404. Example: `POST /api/settings/daily-recap/test` with an
   undecryptable Slack token would answer `400 Slack not connected` where
   the bundle documents only a 500 for a stored-data failure. Fix: read
   functions never return a decryption failure as their error (6.2); five
   named mappings (1); AC-16 body = the operation's documented 500 body
   (`sendDailyRecapTest` documents `{"error":"db error"}`).
3. **K-3 (high) `active_dek INTEGER NOT NULL` vs start order.** 5.3 step 4
   writes the heartbeat before the first data key exists on a fresh
   database: the insert would fail and the backend never starts. Fix:
   nullable (4.1, AC-27).
4. **K-4 (high) Heartbeat presence is not a fence.** A backend whose DB link
   stalls or whose container is paused for > 90 s is "absent" to
   `decrypt-all`/`reset-lost-key`, then resumes and seals under a data key
   those commands deleted: the value is unreadable forever. Write-then-check
   only covers processes that start during the operator run. Fix: 60 s seal
   lease measured from the heartbeat's send time (ends before the 90 s
   window), and shutdown when the active data key disappears or a
   destructive command holds the lock (4.3, AC-21c).
5. **K-5 (medium) Rotation window.** Nothing said `Seal` reads the installed
   keyring at call time or that the heartbeat follows `Install`; a request or
   cron item holding a keyring snapshot could seal under the retired key
   after step 3 swept that row. Harmless (retired keys kept) but it breaks
   AC-11b's "0 values under N". Fix: 5.3 step 7 order, 6.1 comment.
6. **K-6 (medium) Operator and backends configured with different active
   keys.** `rotate-data-key` run with KEK B while backends run KEK A creates
   a data key no backend can unwrap; refreshers cannot switch, the wait
   times out, and every backend restart refuses. Fix: convert/rotate exit 4
   unless the operator's active key wraps the active data key; a refresher
   that cannot unwrap keeps its keyring (8.2, 5.3, AC-31).
7. **K-7 (medium) Compose version.** `env_file` long syntax with `required`
   needs Docker Compose 2.24.0; `docs/runbook.md:22` promises 2.20+. Fix:
   runbook minimum raised, merge condition (5.2, manifest).
8. **K-8 (medium) O-2 had no number.** Fix: 2^30 seals per data key,
   `sealed_count`, automatic rotation step 1 at the bound, `status` flag
   (4.1, AC-29).
9. **K-9 (medium) `operator_audit` copy drift.** PAC-84 copies PAC-71's DDL;
   `CREATE TABLE IF NOT EXISTS` silently keeps whichever merged first. PAC-71's
   CHECK has exactly three actions today (`033:63`), so nothing is lost now;
   a later DDL change on either side would be. Fix: pinning test, restate-all
   rule (7.1, AC-32).
10. **K-10 (low) Kept secrets other than settings.** SSO keeps
    `current.OIDCClientSecret` (`sso_providers.go:199-203`), org AI keeps
    its key, personal calendar `url = COALESCE($4, url)`. Fix: byte-identical
    rule for every save (6.2, AC-16c).
11. **K-11 (low) O-5.** `""` cannot be observed: `personalCalendarDTO.URL` is
    `omitempty` (`handlers_personal.go:37`), bundle says "Omitted when
    empty". Decided: omitted (9.3).
12. **K-12 (low) O-3.** Decided: committed e2e key bytes refused unless
    `PACEDAY_ENCRYPTION_ALLOW_TEST_KEY=true` (no deployment-mode variable
    exists in `backend/`) (9.1, 11, 12, AC-30).
13. **K-13 (low) O-1 and invocation.** PAC-71's `backend/Dockerfile` builds
    `/paceday-operator` and its comment uses `exec backend`, which cannot
    work for subcommands that require the backend stopped; 5.2 named
    compose's `--env-file`, which is the interpolation file, not the service
    `env_file`. Fix: `run --rm --no-deps backend /paceday-operator …`;
    `backend/Dockerfile` added to allowedPaths (5.2, manifest).
14. **K-14 (low) allowedPaths.** Added `backend/Dockerfile`,
    `backend/main_test.go`; migration-header note location; runbook :22.
15. **K-15 (low) `go run .`** (README:40) had no key source (11).
16. **K-16 (low)** lp16 overflow undefined (3.2); `encryption_data_keys.id`
    had no `DEFAULT nextval` and the sequence no owner (4.1).

### Responses the client cannot handle

None. The bundle is unchanged; the only response-value difference is an
omitted optional `url` (already "Omitted when empty"); the web reads
`raw.url` (`smart-calendar-flow/src/api/client.ts:648`) as optional.

### Consumers affected

As listed in the manifest, plus: `docs/runbook.md:22` (Compose minimum),
`backend/Dockerfile` (if PAC-84 merges first), PAC-71's operator doc
`exec` invocation for the destructive encryption subcommands.

### Uncertainties not actually resolved

None claimed by the manifest (`resolvesUncertain: []`).

### Verified clean

- **AAD** (3.2): fixed tag, version byte, every field lp16, part count u8:
  injective; data-key id outside the AAD is fine (another id is another key).
  Wrap AAD binds id and `kek_id`.
- **Row identities** (3.3): `oauth_tokens` `UNIQUE (user_id, calendar_id)`
  (`017_…:11-12`), only writer `token_store.go:25-33` with literal
  `'primary'`; `settings.user_id` NOT NULL (`024_…:144`) + unique (`018_…:22`);
  `workspace_connections` `UNIQUE (user_id, provider)`; `personal_calendars.id`
  SERIAL (`004_…:2`); `sso_providers.id` UUID (`008_…:2`). No `UPDATE` of
  any identity column in non-test Go or later migrations on `main` or the
  PAC-71 branch; PAC-71 binding and its operator steps (`operator.go:45-58`)
  delete or empty secrets, never move rows between users, so no re-keying
  case exists. Every insert-assigned-id writer can do the one-transaction
  write: `DBTX` has `BeginTx` (`dbtx.go:12-20`); personal calendars can take
  `nextval` in one statement; `UpsertSSOProvider` has no non-test caller.
- **`xmin` compare-and-set** (8.3): sound on Postgres 16 (images
  `postgres:16-alpine`, `16.15-alpine`) with SELECT and UPDATE in one READ
  COMMITTED transaction (HOT updates change `xmin`; EvalPlanQual re-checks;
  freezing keeps raw `xmin`; wraparound needs 2^31 xids in one short
  transaction). `SELECT … FOR UPDATE SKIP LOCKED` would be equally sound;
  `xmin` kept (catches other-column writes, holds no row locks).
- **Lock race at start**: operator (lock, then read heartbeats) vs backend
  (commit heartbeat, then read `pg_locks`): whichever second sees the other.
  Correct; the gap was liveness (K-4).
- **Migration order**: PAC-71 adds no operator_audit action beyond its three;
  drop/re-add of `operator_audit_action_check` works in both orders and in
  `TestMigrationsDownTo005AndUpAgain`'s down/up cycle (both downs keep the
  table, `keptByDown`); a migration body is one implicit transaction.
- **Documented 500s**: of the 20 operations without a 500, none acts with a
  stored secret (re-ran the bundle scan). `ssoOidcCallback`/`startSsoLogin`
  document 500 and their 404 "also when the lookup errors" stays true once
  the lookup cannot fail on decryption; `testLLMConnection` and
  `parseNaturalLanguage` 500 texts ("settings load failure" / "could not be
  loaded") cover an undecryptable key decided before the 409/200 branches.
- **backup.sh**: refusal sits after `[ -f "$env_file" ]` (`backup.sh:55`)
  and before `mkdir -p` (`:75`) and `cp` (`:87`); exit 2 matches its other
  input refusals.
- **Manifest conventions** match PAC-72 / PAC-81 part A (added
  `ownerDecided`, `followUps`).
