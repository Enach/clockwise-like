# Contract: PAC-82, concurrent double booking, use-limit race, idempotent retry

- **Linear**: PAC-82, https://linear.app/paceday/issue/PAC-82
- **Spec**: `docs/specs/PAC-82.md` on `factory/pac-82-spec` (`65e1fa2`), accepted
  2026-11-14 (self-challenged, accept-with-changes applied in place).
- **Split**: spec issue item 3 (provider-side idempotency for host calendar
  events and personal-sync blockers) is **PAC-108**
  (https://linear.app/paceday/issue/PAC-108), linked to PAC-82 as related
  (spec OQ-1, D-12). Nothing in this contract depends on it.
- **Manifest**: `contracts/features/PAC-82.yaml`.
- **Fragment**: `contracts/openapi/paths/booking.yaml` only.
- **Base**: `origin/main` `d520775` (bundle of main recorded in the manifest).
- **Revision**: 1.1 (revision 1 by contract-author, 1.1 after the challenge below,
  same day; see "Challenge — 2026-11-14"). Amended by PAC-116 (2026-11-14):
  dated note in §4.4 (migrations rule, row "save / DDL on a live-read table");
  no revision bump, no fragment change, `contractHash` unchanged.

## 0. The rule this contract fixes

A public booking is decided again at the moment it is saved, inside the save's
transaction, after the save has been ordered against every other save it could
conflict with. Concurrent requests are therefore answered as if they had
arrived one after the other. A client can label an attempt with a key, and
sending that attempt again never creates a second booking.

## 1. What changes at the HTTP boundary

Only `createPublicBooking` changes. No operation is added or removed.

| Change | Where |
|---|---|
| New optional request header `Idempotency-Key` (`components/parameters/BookingIdempotencyKey`): 16..255 chars of `A-Za-z0-9-_`, case-sensitive, per link, 24 h window, empty = absent | parameter, operation description |
| New 400 message `invalid Idempotency-Key header` (bad format, quoted value, or the header on 2+ field lines); after the 404, before the body | 400 |
| 201 can be a **replay**: same `id`, `link_slug`, `start`, `end`, `duration_minutes`, `booker_name`, `booker_email`, `notes` as the original; `title`, `hosts` are the link's current values; nothing written or sent; no replay marker | 201 |
| 409 schema becomes `PublicBookingConflict` = `oneOf` [`BookingSlotUnavailableError` (unchanged), `BookingIdempotencyKeyReusedError` (new, `{"error":"idempotency key already used"}`)] | 409, schemas |
| 409 `taken` / `host_busy` and 410 also answer a concurrent loser (same bodies) | 409, 410 |
| 404 also answers a keyed retry on a link deleted or paused since the original | 404 |
| 500 adds the 10 s wait on another booking's save | 500 |
| CORS: `Access-Control-Allow-Headers: Content-Type, Authorization, Idempotency-Key` (server-wide value, `backend/api/middleware.go:250`) | operation description |
| Stale text removed: "Not guaranteed: two concurrent requests..." and "Still open (API-051...)" (`booking.yaml:154-159` on main), the 201's "A retry behaves as before PAC-81 ..." (`:207-210`), and PAC-101's "Bookings of the same link are never made to wait for each other" (`:183`, now false: spec AC-5/AC-8 amend PAC-101 AC-16) | description, 201 |

### 1.1 Order of evaluation

Without a key (or with an empty one): unchanged (spec AC-18):
429 → 404 → 410 → body 400s → zone 500 → cheap 409s → `taken` 409 → host
reads (`host_busy` 409 / 503) → save (which may now answer 409 `taken` /
`host_busy`, 410, 404 link gone, 500).

With a key:
429 → 404 → key 400 → **link-independent** body 400s (invalid body; required
fields; start/end format; duration does not match end; length not whole
minutes; end not after start) → **replay decision** (201 replay / 409
key-reused / none) → 410 → `invalid duration` 400 → zone 500 → cheap 409s →
`taken` 409 → host reads → save (whose own replay decision is authoritative,
§3 step 6).

`invalid duration` moves after the replay for keyed requests because it depends
on the link's current duration list: a retry after the owner removed a
duration must still replay (challenge C-4). The spec's §9.4 order put all body
400s before the replay.

Every refusal (409 or 410) that a **keyed** request is about to answer before
reaching its save is preceded by one more lookup of its key; a booking saved
with that key meanwhile turns the answer into the replay (or the key-reused
409). Without it a retry racing its own original's commit is told "slot taken"
for its own booking (challenge C-3).

What a keyed refusal proves (challenge C-8): when the refusal was decided, no
booking with that key existed on that link. It does not prove that an earlier
request with the same key, still being handled, will not save afterwards; that
can only happen when the earlier request had already reserved its save
connection, and then within its save (the 10 s lock bound plus the insert).

### 1.2 Replay matching (binding)

Match = same link (by id), same key (bytes), and, against the stored row:
`booker_name` = trimmed `name`; `booker_email` = trimmed `email` (byte
equality, no case folding); `start_time` = `start` and `end_time` = the
resolved end, as instants (microsecond precision of the column; a request
instant with sub-microsecond digits never matches); `notes` = `notes` as sent
(absent = `""`). The resolved end uses the link's settings at the time of the
retry only when the body sends neither `end` nor a non-zero length (falls back
to the link's first duration); the web always sends `end`. Age = database
`now() - created_at`, replay only while `< 24 hours`.

Found and matching → replay. Found and not matching, or age `>= 24 h` → 409
key-reused. Not found → new request. A stored row's `status` is not looked at
(no product path cancels a public booking; when one is added, this rule must
be revisited). A row whose booker was erased (PAC-40 part B) never matches
(blank name), but erasure is ≥ 90 days after the link's last meeting, far past
the window.

## 2. Decisions

### 2.1 Header, not body field (spec D-5)

The key is metadata of the attempt, not of the booking. A header keeps
`CreatePublicBookingRequest` (`additionalProperties: false`) and its replay
comparison unchanged (the key is never one of the compared details), and it
matches the IETF draft and common practice. Rejected: a body field
`idempotency_key` (it would widen the closed request schema and mix the
attempt's label into the booking's details). Cost of the header: CORS (§1).

### 2.2 409 for a reused key (spec D-7, OQ-3)

The draft recommends 422 for a reused key with a different payload and 409 for
a request still in flight. Here a same-key request in flight is not refused at
all (it waits, §3), so 409 is free, and the spec's AC-11 fixes it. The body is
a separate schema in a `oneOf` (OQ-3) rather than a seventh value of
`BookingSlotUnavailableError.error`: that schema's meaning is "the slot was
refused" and its description pins (error, reason) pairs; a key error is not a
slot refusal and must look the same whatever `refusal_message` is. PAC-72
rejected `oneOf` for generic vs specific because those variants overlap; these
two have disjoint `error` sets, which is the PAC-50 `SsoWriteForbiddenResponse`
pattern already in the contract. The web keeps importing
`BookingSlotUnavailableError` (`PublicBooking.test.tsx:8`) unchanged.

### 2.3 Replay body built from the stored row (spec §9.4)

Storing the full original response would freeze `title` and `hosts`. The
fields that identify the booking are identical by construction; the link-level
fields are current, as on every 201. No `Idempotent-Replayed` header: the web
shows the confirmation either way, and an exposed header needs another CORS
change.

## 3. The save transaction (non-HTTP interface, binding)

Replaces PAC-101 contract §3.1 step 5. One explicit transaction on the reserved
connection, after `budget.Commit` and the `afterBookingCommit` seam, on the
detached context, **isolation READ COMMITTED** (the default of
`conn.BeginTx(ctx, nil)`; REPEATABLE READ or SERIALIZABLE would take the
snapshot before the waits and miss the winner, challenge C-2). Each numbered
item is its own statement (or statements), in this order:

1. `SELECT set_config('lock_timeout', '<n>ms', true)` (PAC-101 §4.3, unchanged).
2. `SELECT owner_user_id, usage_type, max_uses FROM scheduling_links WHERE id = $1 AND active AND deleted_at IS NULL FOR SHARE`.
   Zero rows → rollback, **link gone** (404).
3. Lock host set: `owner_user_id` ∪ `user_id` of `scheduling_link_hosts` with
   `status = 'accepted'` (plain read). Rule host set (for step 8):
   `GetAcceptedHosts`, exactly as the PAC-72 rule.
4. Compute the advisory keys (§4) and take them, **one statement per key, in
   ascending (class, objid) order**, duplicates removed:
   `SELECT pg_advisory_xact_lock($1::int, $2::int)` or
   `SELECT pg_advisory_xact_lock_shared($1::int, $2::int)`.
   A single statement taking several keys is forbidden (evaluation order of a
   set of calls in one statement is not a contract of PostgreSQL).
5. `AfterBookingLocks` seam (§6).
6. With a key: `SELECT id, booker_name, booker_email, start_time, end_time, notes, now() - created_at >= interval '24 hours' FROM bookings WHERE link_id = $1 AND idempotency_key = $2`.
   Found → rollback; matching (§1.2) → **replayed** (the stored row);
   otherwise → **key reused**. No completion unit either way.
7. Use-limited link (`single_use`, or `recurring` with non-null `max_uses`):
   `SELECT count(*) FROM bookings WHERE link_id = $1` (all statuses) ≥ limit
   (1 or `max_uses`) → rollback, **exhausted** (410).
8. `SELECT EXISTS (... same link, status <> 'cancelled', start_time < $end AND end_time > $start)`
   → rollback, **taken**; else, for the rule host set, the predicate of
   `GetBusyBookingsForUser` restricted to `[start, end)` (any link, deleted or
   not, the host owns or co-hosts as accepted) → rollback, **host busy**.
   Raw overlap, no buffers (buffers are for calendar events). When the rule ever
   widens bookings, the buckets of §4 must cover the widened interval.
9. `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes, idempotency_key) VALUES (...) RETURNING ...`
   (the key column `NULL` without a key). SQLSTATE `23505` on
   `bookings_link_idempotency_key` → rollback, then read the row by (link,
   key) on the pool and resolve as step 6 (replayed or key reused).
10. `COMMIT` → **saved**: connection released, then the completion unit starts
    (PAC-90 unchanged).

**The re-checks (6, 7, 8) must be separate statements issued after the last
lock statement has returned.** Observed on PostgreSQL 16: a statement that
takes the advisory lock and checks for the winner's row in the same statement
reads with the snapshot taken before the wait and does not see the winner
(challenge C-2). Folding steps 7-9 into one `INSERT ... WHERE NOT EXISTS`
issued after step 4 is allowed (its snapshot is taken after the waits).

Outcomes are distinct results of the engine, never `nil, nil` (recommended:
`storage.ErrLinkGone` as today, `ErrSlotTaken`, `ErrHostBusy`,
`ErrLinkExhausted`, `ErrIdempotencyKeyReused`, and a `Replayed` flag on the
returned booking). The handler maps them to 404, 409 (`taken` / `host_busy`
with the link's `refusal_message` read when handled), 410, 409 key-reused, 201.
Any other error, `55P03`, `40P01` or `53200` → the 500. A replay and every
refusal start **no** completion unit.

The handler-level replay decision (§1.1) is a plain read on the pool, by
(link id, key), outside the transaction; it is an optimisation and a
precedence rule (replay before 410 / notice / availability, spec AC-10), not
the guarantee: steps 6 and 9 are.

## 4. Advisory keys and lock order (non-HTTP interface, binding)

### 4.1 Key space

Two-integer form `pg_advisory_xact_lock(int4, int4)` (pg_locks `objsubid = 2`),
transaction-scoped only. Classes reserved for PAC-82:

| Class | Key | Mode |
|---|---|---|
| `8201` | host-wide: `h32("pac82:host:" + host)` | shared (fine save) or exclusive (coarse save) |
| `8202` | host bucket: `h32("pac82:bucket:" + host + ":" + bucket)` | exclusive |
| `8203` | link uses: `h32("pac82:uses:" + link)` | exclusive, only on use-limited links |

`h32(s)` = the first 4 bytes of SHA-256 of the UTF-8 string `s`, big-endian,
as a signed int32. `host` and `link` are the canonical lowercase hyphenated
UUID; `bucket` is decimal. The derivation is fixed so that every replica and
every version agree; changing it is a contract change (a rolling deploy with
two derivations has no guarantee).

Other advisory users: PAC-84 class `8400` (`(8400,1)` session maintenance,
`(8400,2)` xact key setup, `storage/encryption_keys.go:19-21`); nothing else in
`backend/` or `mcp/` (grep, 2026-11-14). Not a 64-bit hashed `bigint` key: a
`bigint` key K shows in `pg_locks` as `classid = K >> 32, objid = K & 0xffffffff,
objsubid = 1`, and PAC-84's presence query
(`encryption_keys.go:130`, `classid = 8400 AND objid = 1`, no `objsubid`)
matches such a key; with a fixed class there is no overlap by construction
(challenge C-6).

A 32-bit hash collision (two different hosts or buckets sharing a key) only
makes two saves wait for each other; never a wrong answer, never a deadlock
(the order is by the key value itself). Within one save, duplicate keys are
taken once.

### 4.2 Buckets

`bucket = floor(t / 900 s)` on the UTC Unix time in microseconds. A slot
`[start, end)` touches buckets `floor(start/900s)` .. `floor((end - 1 µs)/900s)`
inclusive. Two overlapping slots share an instant and therefore a bucket; two
slots at least 15 minutes apart (end of one to start of the other) touch
disjoint buckets, whatever their alignment, length, the link's
`window_start_time` (slots on a 09:10 grid touch two buckets each) or zone
(buckets are UTC, so DST and :30/:45 offsets do not matter). Slots touching end
to start (10:00-10:30, 10:30-11:00) do not overlap and may or may not share a
bucket. A slot is at most one daily window long (≤ 25 h on a fall-back day), so
at most 101 buckets.

### 4.3 Fine and coarse saves (lock-table bound)

`n = |lock host set| × |buckets|`.

- **Fine** (`n ≤ BookingKeyCap`, 64): per host, `8201` **shared**; per (host,
  bucket), `8202` exclusive.
- **Coarse** (`n > 64`): per host, `8201` **exclusive**; no `8202`.
- Then, on a use-limited link, `8203` exclusive.

Coarse saves conflict with every save of their hosts (exclusive vs shared on
`8201`), fine saves only through common buckets. Keys per save ≤ 64 + hosts + 1.
Why a cap: advisory locks live in PostgreSQL's shared lock table; with the
default settings (`max_locks_per_transaction` 64, `max_connections` 100) it held
about 11-14 thousand advisory locks across all sessions in the challenge, and
past that **every** session's lock request fails with `53200 out of shared
memory` (observed). Uncapped, one 24 h slot on a 5-host link is 485 keys, and
20 such saves (one pool) approach the limit. Cost: a coarse save (e.g. 2 hosts
and a slot over 8 h) makes bookings of those hosts at other times wait for one
save's duration, and a fine save queued behind a coarse waiter waits too
(PostgreSQL queues a compatible request behind a conflicting waiter, observed).
This narrows spec AC-5 to fine saves (manifest AC-5).

### 4.4 Lock order and deadlock table

Booking save: link `FOR SHARE` → advisory keys ascending by (class, objid)
(`8201` < `8202` < `8203`) → booking row and its unique-index entry (FK: link
`KEY SHARE`, covered by the `FOR SHARE` already held). Every key is taken before
any statement that can wait on a row or an index entry; reads in steps 3, 6,
7, 8 are MVCC reads and never wait.

| Pair | Wait? | Cycle? |
|---|---|---|
| save / save, same link or shared host | advisory keys (total order, shared and exclusive); the unique index only after all keys | No: a key waiter holds a strict prefix below the key it waits for; an index waiter (step 9) waits for a transaction past its key phase that waits for nothing |
| save / save, coarse vs fine | `8201` exclusive vs shared, same total order | No |
| save / delete or pause (PAC-40, `NO KEY UPDATE`) | at step 2 only, before any key | No: the delete takes no key |
| save / PATCH (PAC-101, `NO KEY UPDATE` or `UPDATE` → host rows) | at step 2 only | No |
| save / invite, accept, decline, leave (`FOR SHARE` → host row) | compatible on the link; a save never locks a host row | No |
| save / PAC-40 erasure (link `FOR UPDATE` → bookings → owner `KEY SHARE`) | only on a deleted link, which step 2 filters out without locking it | No |
| save / PAC-71 `users` writers | the save locks no `users` row | No |
| save / PAC-84 (`8400`) | disjoint classes; no key load inside a save (bookings are not encrypted) | No |

A delete or PATCH that queues behind a save that is itself waiting on a key
waits at most its own 10 s bound (PAC-101 §2.4). AC-24 asserts no `40P01`.

- **Amended by PAC-116 (2026-11-14, spec `docs/specs/PAC-116.md`, contract
  `contracts/features/PAC-116.md`; owner decisions Q1–Q3 of 2026-11-14;
  no revision bump).** The table above has no row for a migration. Migrations run
  against a database that may be serving traffic: at server start with other
  backends still live, and from `paceday-operator encryption` (every
  subcommand calls `storage.Migrate`, `cmd/paceday-operator/encryption.go:181`).
  Each up file runs as one transaction. Read this row as part of the table:

  | Pair | Wait? | Cycle? |
  |---|---|---|
  | save / DDL on a live-read table (a migration touching `bookings`, `scheduling_links`, `scheduling_link_hosts` or `users`) | the save waits at step 2 (link `FOR SHARE`), at its reads or at its insert while the migration holds a conflicting lock; the migration waits for the save's locks to be released | No cycle on that one table, **under the migrations rule below** (a migration that locks two of these tables can still form a cross-table cycle: see "Not covered"). Without the rule: yes. Example: a migration holding SHARE on `bookings` (`CREATE INDEX`) asks for ACCESS EXCLUSIVE (`ALTER TABLE`) while a save holding ACCESS SHARE (step 6–8 reads) waits for ROW EXCLUSIVE (step 9 insert). Both wait on each other, and one is aborted with `40P01` |

  **Migrations rule (binding).** Consider a migration file that touches one
  of those four tables, where the table existed before the file runs. It takes
  its strongest lock on that table before any weaker lock in the same
  transaction. Strength is measured by the level at which the lock blocks
  what live paths take (ACCESS EXCLUSIVE > EXCLUSIVE > SHARE, SHARE ROW
  EXCLUSIVE > everything else). The full definition, the statement
  classification (including locks a statement takes on a second table, such as
  an FK referencing `users`) and the test that enforces it are in
  `contracts/features/PAC-116.md` §2–§5. A migration meets the rule in one of
  two ways: its ACCESS EXCLUSIVE statements come first, or it starts with
  `LOCK TABLE <table> IN ACCESS EXCLUSIVE MODE`. Transaction control and
  `CONCURRENTLY` are not allowed in an up file. There is no exemption.
  Migrations 001–039 comply (037 and 038 take ACCESS EXCLUSIVE first).

  **Not covered (advice, PAC-116 OQ-1).** The rule orders locks on one
  table. A migration that locks two of these tables can still deadlock if it
  takes them in an order different from a live path's. Take them in the save's
  order, `scheduling_links` → `scheduling_link_hosts` → `bookings`. Do not
  combine `users` with another of them at SHARE or stronger outside a
  maintenance window: erasure locks `users` last, account deletion first.

  The approved text and table above are kept as revision 1.1 approved them.

### 4.5 Wait bound (spec OQ-2: answered)

`lock_timeout` caps a `pg_advisory_xact_lock` wait, a
`pg_advisory_xact_lock_shared` wait and the unique-index wait on another
transaction's uncommitted key, each with SQLSTATE `55P03` (observed on
PostgreSQL 16.15, §Challenge). The PAC-101 bound (`storage.LinkLockTimeout`,
10 s, per lock acquisition) therefore covers every new wait; past it the
transaction rolls back and the answer is the 500, nothing written.

## 5. Migration (binding)

Next free number on main: **038** (main's latest is 037; no `origin/*` branch
carries 038+ on 2026-11-14). Name `038_booking_idempotency_key`.

```sql
-- up
ALTER TABLE bookings ADD COLUMN idempotency_key TEXT;
ALTER TABLE bookings ADD CONSTRAINT bookings_idempotency_key_format
  CHECK (idempotency_key IS NULL OR idempotency_key ~ '^[A-Za-z0-9_-]{16,255}$');
CREATE UNIQUE INDEX bookings_link_idempotency_key
  ON bookings (link_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
-- down
DROP INDEX IF EXISTS bookings_link_idempotency_key;
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_idempotency_key_format;
ALTER TABLE bookings DROP COLUMN IF EXISTS idempotency_key;
```

Every existing row gets `NULL`, so the index and the check build on any data,
including existing duplicate and overlapping bookings (spec AC-21, §9.1);
observed on a database migrated 001..037 holding two identical bookings of one
link and a cross-link overlap. No exclusion constraint, no `btree_gist`, no
repair of existing rows. Not `CONCURRENTLY` (golang-migrate runs the file as
one implicit transaction; the build takes a `SHARE` lock on `bookings` for its
duration at startup, before the server listens). The down loses the keys only
(AC-22). No new advisory lock or extension.

`storage.CreateBooking` (no locks, no key) stays a test helper; no production
path may call it (grep: none today).

## 6. Test seams (names may differ; behaviour may not)

| Seam | Where | Used by |
|---|---|---|
| `afterBookingLinkRead` (exists, api) | after the link read | AC-14 (hold A before its pre-checks) |
| `afterBookingCommit` (exists, engine) | after `budget.Commit`, before the save transaction | AC-1, AC-2, AC-3, AC-7, AC-16 (release N requests together past their pre-checks) |
| `AfterBookingLocks` (new, storage) | inside the save, after step 4, before step 6 | AC-4, AC-8, AC-15, AC-23 (hold a save holding its keys) |
| `AfterBookingInsert` (new, storage) | after step 9, before `COMMIT`; a non-nil error rolls back | AC-4 and AC-15 rollback halves |
| `BookingAdvisoryKeys(lockHosts, linkID, useLimited, start, end)` (new, exported pure function) | §4 derivation | AC-5, AC-6 assert their two key sets are disjoint before asserting "no wait" (a 32-bit collision would otherwise flake them); unit tests of buckets and the cap |
| `BookingKeyCap` (new package variable, 64) | §4.3 | coarse-mode test with few hosts |
| `LinkLockTimeout` (exists) | PAC-101 | AC-23 |

Waits are observed in `pg_locks` (`locktype = 'advisory' AND NOT granted`) or
`pg_stat_activity` (`wait_event_type = 'Lock'`), never by sleeping alone. Key
age (AC-12) is set by `UPDATE bookings SET created_at = now() - interval '25 hours'`
in the test.

## 7. Consumers

- **smart-calendar-flow** (`origin/main` `d935bcc`): `bookSlot`
  (`src/api/schedulingLinks.ts:539-556`) sends `{name, email, start, end, duration, notes}`
  through `requestApi` (`src/api/client.ts:588`), which cannot add a header
  today: the web PR adds the key (spec AC-25..AC-27). Generated types change
  (header parameter, 409 `PublicBookingConflict`, descriptions): a
  regeneration commit paired with the stage-4 PR. `PublicBooking.test.tsx:8`
  imports `BookingSlotUnavailableError`, which is unchanged. PAC-83 AC-21's
  test (`PublicBooking.pac83.test.tsx:184-211`) is amended by spec AC-26.
- **backend/api/gen**: `CreatePublicBookingParams` gains `IdempotencyKey`, the
  409 response type becomes the `oneOf` union; the hand-written handler does not
  use the strict server for this route (MIGRATION.md: handwritten), so nothing
  breaks.
- **mcp/**: no booking call. **e2e/**: keyless bookings (`edge.spec.ts:86`,
  PAC-72/PAC-40 journeys) unchanged (AC-18).
- **Backend fakes of `BookingFlow`** (`api/handlers_booking_test.go:59` and the
  four listed in PAC-101 §8): `ConfirmBooking` gains the key argument; the
  fakes change with it.
- **PAC-101 AC-16 test** (bookings never wait): amended by AC-5/AC-8.

## 8. Rollback

Revert the backend PR; the down migration drops the key column, the check and
the index; bookings are never deleted. The concurrency guarantees and replays
end. Web: revert independently; a keyed web against an old server is refused
by the old CORS on cross-origin deployments (same-origin deployments ignore the
header), so the web merges after the server and reverts before it.

## 9. Alternatives rejected

- **Exclusion constraint** (spec §9.1): fails on existing overlaps, per link
  only, needs `btree_gist`.
- **SERIALIZABLE**: retries on `40001` at every caller; serializes unrelated
  bookings on predicate locks; and the conflict spans links through hosts,
  which predicate locks on `bookings` would catch only by scanning broadly.
- **One advisory lock per host** (no buckets): serializes every booking of a
  host, breaking AC-5 for all links. Kept only as the coarse mode.
- **64-bit hashed `bigint` keys**: §4.1.
- **No cap on keys**: §4.3 (`53200` is server-wide).
- **A key table separate from `bookings`** (key, link, request hash,
  response): needs its own expiry job and a second write; the booking row
  already holds everything a replay needs, and PAC-40 erasure need not touch a
  random key.
- **Replay decided only in the handler** (spec C-1): misses a same-key request
  still before its save.
- **Replay decided only in the save**: a replay would then come after the 410
  and the availability checks (spec AC-10 fails) and would cost host calendar
  reads.
- **422 for a reused key** (the IETF draft): §2.2.
- **Accepting the draft's quoted form too**: two spellings of one key; the web
  is the only client and sends a bare UUID.
- **A replay marker header**: §2.3.

## 10. Gate results (contract stage)

Regenerated in an LF clone (`golang:1.25-bookworm`, oapi-codegen v2.4.1, the
api-gate pattern, host repo mounted read-only), 2026-11-14, at `0fb0480`:
`make openapi WEB=/nonexistent` rc 0 (132 operations, 12 generated);
regenerated `contracts/openapi/openapi.yaml` and `backend/api/gen/paceday.gen.go`;
`make openapi-check WEB=/nonexistent` rc 0 ("bundle is in sync (110 paths)",
"MIGRATION.md covers all 132 operations (12 generated)", "backend/api/gen
matches the contract"; web half SKIPPED by design); `cd backend && go build ./...`
ok. Bundle sha256
`85187c9448b95f89e3ce05ac334d53ec9cf1bb0f896bb89d788ce46726adbdd4`
(= `contractHash`; main `d520775`: `3fa2668c…`). Not run: test suites, lint and
coverage (no hand-written Go changed).

## Challenge — 2026-11-14

**Verdict**: accept-with-changes. The changes are applied in revision 1.1 (this
document, the manifest, and the fragment text committed at `0fb0480`, whose
bundle is `contractHash`).

The contract-challenger role was played by the same agent, which re-read
`origin/main` `d520775` (handlers, engine, storage, middleware), the web at
`d935bcc`, and ran every locking and migration claim on a throwaway PostgreSQL
16.15 container on an internal docker network with no published port, against a
database migrated with the real files 001..037 (each in one transaction, as
golang-migrate runs them).

### Requests wrongly permitted

1. `POST /api/book/s` with `Idempotency-Key: "8e03978e-40d5-43e8-bc93-6894a57f9324"`
   (the draft's quoted structured-field form). Revision 1 said "the IETF
   header" without a form; a server following the draft would strip the
   quotes, one following the pattern would answer 400. Fixed: bare token only,
   quotes are the 400 (parameter description, §9).
2. The header on two field lines (`Idempotency-Key: aaaa…` twice). Go's
   `Header.Get` reads the first and ignores the second, so the server and an
   intermediary could disagree on the key. Fixed: two or more lines = 400.
3. `Idempotency-Key:` with an empty value. The schema (`minLength: 16`) forbids
   it; the server treats it as absent (spec AC-17). Stated in the parameter
   description as tolerated, not part of the schema.

### Responses the client cannot handle

1. The 409 is now a `oneOf`. `PublicBooking.tsx` (web `d935bcc`) treats every
   409 as a slot refusal (refetch the day, clear the slot). It never sends a
   key, so it never receives the key-reused body; the web PR that adds keys
   must branch on `error` (AC-27). Generated Go gets an opaque union, harmless:
   the route is handwritten.

### Consumers affected

- Web generated types (header, union, descriptions): regeneration paired with
  stage 4. `PublicBooking.test.tsx:8` keeps compiling (schema name unchanged).
- PAC-83 AC-21 test, PAC-101 AC-16 test
  (`TestPAC101_SavesOnOneLinkDoNotWaitForEachOther`): amended, listed in the
  manifest.
- `mcp/`, `e2e/` keyless journeys: none.

### Contract/implementation divergences (fixed in 1.1)

1. **C-1 (blocking) spec OQ-2 answered by observation.** With a holder in
   another session, `SET LOCAL lock_timeout='300ms'; SELECT pg_advisory_xact_lock(8201,42)`
   gives `ERROR 55P03 canceling statement due to lock timeout`; same for
   `pg_advisory_xact_lock_shared`. The waiter shows `wait_event_type='Lock'`,
   `wait_event='advisory'` and a `pg_locks` row
   `advisory|8201|42|2|ExclusiveLock|f`. The unique-index wait on another
   transaction's uncommitted key times out too: `55P03 … while inserting index
   tuple … in relation "bookings_link_idempotency_key"`. When that holder
   commits, the waiter gets `23505`; when it rolls back, the waiter's insert
   succeeds. §4.5.
2. **C-2 (blocking) stale snapshot.** Observed in READ COMMITTED:
   `SELECT (SELECT EXISTS(<winner's slot>)) FROM (SELECT pg_advisory_xact_lock(8202,7)) s`
   returned **false** after waiting for a winner that inserted and committed;
   the next statement returned true. A single-statement "lock and check" (or a
   CTE) double-books, and so would REPEATABLE READ. The spec did not say.
   Fixed: §3 (separate statements, READ COMMITTED), AC-31.
3. **C-3 (blocking) a retry refused for its own booking.** Keyed A' does its
   replay lookup (nothing yet), then its original A commits, then A' reaches the
   410 check or `HasOverlappingBooking` and answers 410 or 409 `taken` for the
   booking its own key saved; the web would then say "not saved". The spec's
   in-transaction lookup only covers requests that reach the save. Fixed: one
   more key lookup before any keyed 409 or 410 (§1.1, operation description),
   AC-29.
4. **C-4 (blocking) replay turned into a 400.** The spec's order puts every body
   400 before the replay, but "invalid duration" (`durationAllowed`,
   `handlers_booking.go:401`) depends on the link's current durations: an owner
   who removes 30 min after a 30-minute booking turns its retry into a 400. The
   issue requires the replay before link-state checks. Fixed: for keyed
   requests "invalid duration" moves after the replay and the 410 (§1.1, 400
   description); AC-10 extended.
5. **C-5 (blocking) lock-table exhaustion.** The spec's per-(host, bucket)
   scheme is unbounded in hosts × slot length (up to 101 buckets per host).
   Observed with the default `max_locks_per_transaction` 64 and
   `max_connections` 100: one transaction took 12 000 advisory locks and 14 000
   failed; while one session held 11 000, another session asking for 3 000 more
   failed with `53200 out of shared memory`. The table is server-wide, so a
   burst of long multi-host saves would break unrelated transactions. Fixed: the
   fine/coarse rule with `BookingKeyCap` 64 (§4.3), at most 64 + hosts + 1 keys
   per save. Also observed: a shared request queues behind a waiting exclusive
   one, so the coarse cost is stated. Spec AC-5 is narrowed to fine saves
   (manifest AC-5); AC-30 pins the derivation.
6. **C-6 key space.** PAC-84 is the only other advisory user (class 8400:
   `(8400,1)` session lock, `(8400,2)` transaction lock). Observed: a `bigint`
   key `(8400<<32)|1` and the pair `(8400,1)` are distinct locks (`objsubid` 1
   vs 2; `pg_try_advisory_xact_lock` of the bigint succeeds while the pair is
   held), but PAC-84's presence query (`classid=8400 AND objid=1`, no
   `objsubid`) counted **both**. A 64-bit hashed bigint key can therefore look
   like PAC-84's maintenance lock (2^-64 per key, but nonzero). Fixed:
   two-integer form with reserved classes 8201-8203 (§4.1); the hash and its
   byte order are pinned so replicas agree.
7. **C-7 bucket edges.** The proof holds for any alignment: a slot ends at e,
   another starts at s ≥ e + 15 min, and the first's last bucket
   floor((e − 1 µs)/900 s) is below floor(s/900 s). So AC-5 holds for 09:10
   grids, odd durations (7, 50 min), :45 offsets and DST days, given the
   exclusive end. The engine's overlap is half-open (`overlapsMerged` in
   `engine/booking.go`; SQL `start_time < $end AND end_time > $start`), which
   matches the buckets. Pre-PAC-72 rows with seconds matter only to the
   re-check, which is a plain overlap query. AC-30.
8. **C-8 (open, does not block the contract) what a refused "Try again"
   proves.** Spec AC-27 tells the visitor "the earlier attempt was not saved"
   after a slot 409 or a 410 on Try again. With C-3 fixed, the server
   guarantees that no booking with this key existed on the link when the
   refusal was decided. An original attempt that is still running can save
   afterwards only if it had already reserved its save connection when its
   client lost it. An earlier disconnect cancels `r.Context()`, and the host
   reads and `DB.Conn(ctx)` then fail before anything is written (read from the
   code, not tested). Even then it can only save within its save step (at most
   the 10 s lock bound plus the insert), and only when the refusal was one the
   original would not also hit: a calendar event added at the provider in the
   meantime (`host_busy`), the start crossing the notice, or a settings edit.
   So "not saved" is almost always true but not guaranteed. The wording is the
   spec's (D-9) and is recorded as a follow-up; the contract states the exact
   guarantee in §1.1 (the operation description states the re-lookup). Also found: spec AC-27 does not say
   what a 429 or a 400 on Try again shows. The manifest reads it as: 429 keeps
   "may have been saved" and Try again; 400 "invalid duration" is treated like a
   slot refusal (after C-4 it can only follow a non-replay).
9. **C-9 stale sentence the spec missed.** PAC-101's "Bookings of the same link
   are never made to wait for each other by this ordering" (`booking.yaml:183`)
   becomes false under AC-5 and AC-8. Rewritten.

### Uncertainties not actually resolved

1. `BookingKeyCap` 64 is a factory number, not measured against production
   PostgreSQL settings. The compose files do not set
   `max_locks_per_transaction`, so the defaults observed here apply.
2. C-8's claim about a disconnected original is read from the code
   (`budget.Commit`, `ConfirmBooking`), not covered by a test; AC-28 covers only
   the saved-then-dropped case.
3. Spec D-1..D-12 remain factory-assumed (the spec raised no owner question).
   The AC-5 narrowing (C-5) and the keyed 400 order (C-4) are contract-stage
   amendments of accepted criteria, recorded here and in the manifest.

### Verified clean (PostgreSQL 16.15, two or three sessions, READ COMMITTED)

- Migration 038 on a database at 037 holding two identical bookings of one link
  and a cross-link overlap: up ok, rows unchanged, every key NULL; a second row
  with the same (link, key) gives `23505`; the same key on another link is
  accepted; a 15-character key and a key ending in a newline violate the check
  (`~` with `$` does not match before a trailing newline); down keeps all 5
  rows; up again ok.
- Ordered mixed-mode locking: T1 (8201 shared, 8202 exclusive), T2 (8201
  exclusive, waits), T3 (8201 shared, queued behind T2): all three complete in
  order, no deadlock.
- Lock order against PAC-101 (link `FOR SHARE` first, unchanged), PAC-40 part B
  (erasure works only on deleted links, which the save's step 2 filters out
  without locking), PAC-71 (no `users` lock in the save) and PAC-84 (disjoint
  classes; bookings are not encrypted, so no key load inside a save): §4.4. The
  insert's FK `KEY SHARE` is covered by the `FOR SHARE` already held, so after
  the keys nothing waits on a row except the unique index, whose holder is past
  its key phase.
- The lock host set covers every host whose busy time can see the booking:
  `GetBusyBookingsForUser` counts links the user owns or co-hosts as accepted,
  which is exactly owner ∪ accepted. The re-check uses the rule host set
  (`GetAcceptedHosts`), so listing and booking stay equivalent (PAC-72).
- Replay before vs inside the transaction: the in-transaction lookup after the
  keys serializes same-key, same-details requests (same link, so same owner, so
  a common `8202` key, or `8201` in coarse mode). The same key with different
  times shares no key and is caught by the unique index (C-1 observed both
  outcomes).
- Web retry: `requestApi` sends only `Content-Type` today (`client.ts:588-606`),
  and `bookSlot` always sends `end` and `duration`, so the web's resolved end
  never depends on the link's default duration.
- `storage.CreateBooking` (a lockless insert) has no production caller.
