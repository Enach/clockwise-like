# Contract: PAC-84 (encrypt stored provider credentials and API keys at rest)

- **Linear**: PAC-84, https://linear.app/paceday/issue/PAC-84/encrypt-stored-provider-credentials-and-api-keys-at-rest
- **Spec**: `docs/specs/PAC-84.md`, challenged accept-with-changes 2026-11-01
  (C-1..C-19 applied), owner Q-1..Q-3 answered 2026-11-01. Branch
  `factory/pac-84-spec` at `3d776ba`.
- **Stage**: contract-author, revision 1, 2026-11-02. Next: contract-challenger.
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
2. **AI paths.** An undecryptable AI key is the documented 500, never a
   silent fall-back to "no key configured" behaviour.

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
makes the encoding injective (no two bindings share bytes). The data-key id is
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
    id          INTEGER     PRIMARY KEY,           -- nextval(encryption_data_keys_id_seq), never reused
    state       TEXT        NOT NULL CHECK (state IN ('active', 'retired')),
    kek_id      TEXT        NOT NULL CHECK (kek_id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$'),
    wrapped     BYTEA       NOT NULL CHECK (octet_length(wrapped) = 60),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    wrapped_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at  TIMESTAMPTZ NULL,
    CHECK ((state = 'retired') = (retired_at IS NOT NULL))
);
CREATE UNIQUE INDEX encryption_data_keys_one_active
    ON encryption_data_keys ((true)) WHERE state = 'active';

CREATE TABLE encryption_backend_heartbeats (
    instance_id UUID        PRIMARY KEY,           -- random per backend process
    hostname    TEXT        NOT NULL DEFAULT '',
    active_dek  INTEGER     NOT NULL,              -- the data key this process seals with
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
  `env_file: [{ path: ./encryption.env, required: false }]` (compose >= 2.24)
  and **neither** compose file names `PACEDAY_ENCRYPTION_KEY` or `_PREVIOUS`
  in `environment:` (an `environment:` entry, even `${VAR:-}`, would override
  the file with an empty value). `required: false` is deliberate: a missing
  file reaches the backend, which refuses with its own message (AC-8, AC-28)
  instead of a compose error that names no runbook section.
- `encryption.env` sits beside `.env`, mode 600, is git-ignored, and is never
  the file passed to `backup.sh --env-file` (section 10 refuses it).
- `paceday-operator` reads the same two variables from its environment; the
  runbook runs it as `docker compose -f docker-compose.prod.yml run --rm
  --env-file … backend /paceday-operator …` or equivalent so it gets the same
  `env_file` (plan picks the exact invocation; the image must ship the CLI
  binary).

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
   data keys, unwrap only ids not yet held, switch the active id, upsert the
   heartbeat with `active_dek`) and the **automatic conversion** (section
   8.3), both as `lifecycle` tracked units so the drain stops them between
   batches.

The KEK is used only in step 4 and by the refresher for a data key id it has
not seen (created by `rotate-data-key` or by another process's first-key
creation). AC-26's counter equals the number of data keys over a run in which
no data key is created.

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

// Process-wide keyring, installed once at start (like calendar.Configure).
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
  a per-field error); the plan picks the type. A settings save writes an
  unchanged secret **byte-identical to what it read** (or omits the column);
  it never re-seals or blanks a value it could not open.
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
column CHECK. No inventory column changes type, default or nullability (D-3).
The up step encrypts nothing (SQL has no key); the conversion does.

### 7.2 Down: `NNN_encryption_at_rest.down.sql`

1. A `DO` block raises
   `PAC-84 down refused: encrypted values or data keys remain; run "paceday-operator encryption decrypt-all" first (docs/runbook.md, "Encryption key - downgrade")`
   if `encryption_data_keys` has any row or any of the 18 columns has a value
   with `left(col, 6) = 'pdenc:'`. The migration runs in one transaction, so
   the refusal changes nothing.
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

### 8.2 Subcommands

| Subcommand | Flags | Key needed | Lock | Does | Audit action |
|---|---|---|---|---|---|
| `status` | `--check` | none | none | Per inventory column: `empty`, `plain`, `encrypted` counts and encrypted per data key; per data key: id, state, wrapping key id, created (age in days), wrapped_at, retired_at, values. Live heartbeats (instance, host, active_dek, seen). Read-only. | none |
| `convert` | `--operator --reason [--batch 100]` | active (+ previous to unwrap) | maintenance | Encrypts every plain value (8.3). | `encryption_convert` |
| `rotate-data-key` | `--operator --reason --retire <dek id> [--batch 100] [--wait 5m]` | active (+ previous) | maintenance; key setup for step 1 | (1) if `<dek id>` is active: in one transaction create a new active data key and mark `<dek id>` retired; (2) wait until every live heartbeat reports the new `active_dek` (none live: no wait), else exit 1 after `--wait` (a re-run resumes); (3) re-seal every value under `<dek id>` with the active key in batches (8.3 compare). Re-run with the same `--retire` after completion: 0 changes. | `encryption_rotate_data_key` |
| `decrypt-all` | `--operator --reason [--empty-undecryptable]` | keys that open every data key | maintenance + no live backend | Writes every sealed value back as plaintext (8.3 compare; the only path allowed to send plaintext, AC-19c). Values that fail to decrypt are listed (table, column, row, dek, reason) and left as they are -> exit 1, data keys kept; with `--empty-undecryptable` they get the AC-23 per-secret removal instead. When no sealed value remains, deletes every data key. | `encryption_decrypt_all` |
| `reset-lost-key` | `--operator --reason --confirm-lost-key` | the **new** active key only | maintenance + no live backend | AC-23 exactly: every sealed value gets its owner's-removal end state, no provider call; plain values untouched; every data key deleted; one new active data key created under the new key. Exit 4 if any configured key opens a stored data key ("the key is not lost"). | `encryption_reset_lost_key` |
| `downgrade-schema` | `--operator --reason` | none | maintenance + no live backend | Runs this work's down step (7.2) via the embedded migrations, only if the database's current version is this work's `NNN` (a later migration applied -> exit 1, "roll back the later release first"). | `encryption_downgrade_schema` |

`downgrade-schema` is factory-assumed (not in D-8): AC-22 requires the runbook
to name the command that runs the down step, and nothing can today (the
backend migrates up only, `storage/db.go:59`). Its audit row is written after
the down step, into the kept `operator_audit`.

### 8.3 Batch passes (conversion, rotation, decrypt-all)

- Per inventory column, batches of `--batch` rows (default 100, 1..1000), one
  short transaction each: `SELECT <row identity>, <column>, xmin::text FROM t
  WHERE <selector> ORDER BY <identity> LIMIT n`, then per row
  `UPDATE t SET <column> = $sealed WHERE <identity> AND xmin::text = $xmin`.
  Selector: convert = plain (non-empty, `left(col,6) <> 'pdenc:'`); rotate =
  `split_part(col, ':', 3) = '<dek id>'` among sealed; decrypt-all = sealed.
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
| lists (personal calendars) | the row is listed; an undecryptable `url` is returned as `""` (note below) |
| disconnect | 200, Zoom revocation `unconfirmed` |

Note on the list row: S5 is returned to its owner today; a row whose `url`
cannot be decrypted is **listed with `url` equal to `""`** and everything
else as stored (factory-assumed: the alternative, omitting the row, would hide
it from the delete button AC-16c(d) requires; returning the ciphertext would
break AC-19). The plan adds a test.

### 9.4 Informational lines

- start: `encryption: key "<id>" active; <n> data keys (active <m>); <r> re-wrapped from previous keys`
- previous key no longer used: `encryption: previous key "<id>" wraps no data key; remove it once the backup retention (14 days) has passed (docs/runbook.md, "Encryption key")`
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
| `e2e/docker-compose.e2e.yml` | `PACEDAY_ENCRYPTION_KEY: ${E2E_ENCRYPTION_KEY:-e2e-test:cGFjZWRheS1lMmUtdGVzdC1rZXktTk9ULVNFQ1JFVCE=}` (32 ASCII bytes `paceday-e2e-test-key-NOT-SECRET!`), same pattern as `E2E_JWT_SECRET`. |
| e2e rotation / wrong-key cases (AC-2, AC-9, AC-11) | the e2e key above plus a second committed constant in `e2e/lib`: `e2e-test-2:cGFjZWRheS1lMmUtdGVzdC1rZXktMi1OT1RTRUNSRVQ=` (`paceday-e2e-test-key-2-NOTSECRET`). |
| `e2e/tests/startup.spec.ts` `BOOTABLE` | adds `PACEDAY_ENCRYPTION_KEY` = the e2e key. |
| production-reference boot (`:72-90`) | `productionReferenceEnvironment()` output plus `PACEDAY_ENCRYPTION_KEY` = the e2e key, added explicitly with a comment saying production gets it from `encryption.env`; the test also asserts, from the YAML text, that the prod backend service declares that `env_file` and that its `environment:` names neither key variable. |
| Go suite (`scripts/test-backend.sh`, testcontainers) | `secretboxtest.Install` / `secretboxtest.Key`: a random key per test process, generated in committed test code; the script itself needs no variable. |
| `scripts/ops/rehearse-backup-restore.sh` | `openssl rand -base64 32` per run into `$work/encryption.env`, outside the backup directory; `launch_backend` gains the key argument; a second random key for the negative control. |

The two committed e2e keys are test-only and appear only under `e2e/`
(and the `#:` line of `E2E_ENCRYPTION_KEY`). Detecting them in production is
**not** in scope (open item O-3).

## 12. `.env.example` and runbook text (AC-24, AC-25)

`.env.example` gains, with **no** `PACEDAY_ENCRYPTION_KEY=` value line:

```
# ── Encryption key (PAC-84) ── lives in encryption.env beside this file, NEVER here
#: PACEDAY_ENCRYPTION_KEY | required | default: none (the backend refuses to start) | <key id>:<base64 of 32 random bytes>; wraps the data keys that encrypt stored credentials. Put it in encryption.env (mode 600), not in .env: backup.sh refuses an env file holding it. Generate: echo "PACEDAY_ENCRYPTION_KEY=$(date -u +%Y%m%d):$(openssl rand -base64 32)" > encryption.env. Losing it loses every stored connection (docs/runbook.md, "Encryption key").
#: PACEDAY_ENCRYPTION_KEY_PREVIOUS | optional | default: empty | Comma-separated retired keys, same format, accepted to re-wrap data keys during a rotation; keep each at least 14 days after retiring it (backup retention). encryption.env only.
#: E2E_ENCRYPTION_KEY | test-only | default: e2e-test:cGFjZWRheS1lMmUtdGVzdC1rZXktTk9ULVNFQ1JFVCE= | PACEDAY_ENCRYPTION_KEY of the e2e stack (e2e/docker-compose.e2e.yml). Never use it anywhere else.
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
| F-15 | Undecryptable personal-calendar `url` listed as `""` | 9.3 |
| F-16 | backup.sh refusal exits 2 (input refusal, like its other argument checks) | 10 |
| F-17 | Committed e2e keys; random keys for Go and the rehearsal | 11 |
| F-18 | One migration text for either merge order with PAC-71 | 7.3 |

## 15. Open items for the challenger / plan

- **O-1** The `paceday-operator` binary must ship in the backend image (PAC-71
  builds it on its branch; check its Dockerfile change) so the runbook's
  `docker compose run` invocation works; plan names the exact command.
- **O-2** Encryptions per data key per year for the documented deployment
  size (D-1, 2^32 random-nonce bound): the plan states the number; `status`
  shows each data key's age so `rotate-data-key` can be run routinely.
- **O-3** Refusing (or warning on) the committed e2e keys outside test mode is
  not specified; proposed as a follow-up rather than added here.
- **O-4** The env-file key is visible in `docker inspect` to whoever controls
  Docker on the host; accepted under Q-1 (KMS later), stated in the runbook.
- **O-5** F-15 (empty `url` in the list for an undecryptable row) is the only
  place a response *value* differs from a stored one; challenger to confirm
  it is preferable to a 500 for the whole list (which AC-16c(d) forbids).
