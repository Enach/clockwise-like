# Implementation plan — PAC-84: encrypt stored secrets at rest

- **Spec**: `docs/specs/PAC-84.md` (challenged, owner Q-1..Q-3 answered 2026-11-01)
- **Contract**: `contracts/features/PAC-84.{md,yaml}` at `c094f83` (interface revision 2,
  K-1..K-17); HTTP bundle unchanged, `contractHash` `a15d981e…ee472`
- **Resolves**: PAC-81 F-D5; `docs/security.md` open risks 4 and 10

## 1. Approach

A new package `backend/internal/secretbox` holds the envelope
(`pdenc:1:<dek>:<b64url>`), the AAD binding (table, column, row identity),
key parsing, data-key wrap/unwrap and an immutable in-memory keyring
installed process-wide. `storage` owns the inventory registry (18 columns)
and seals on every write / opens on every read. Read functions never fail on
one undecryptable secret: settings keep a per-column record (stored text,
opened value, error) so a save writes back byte-identical what it did not
change; other rows carry a per-row `SecretErr`/`TokenErr`. Act paths check
that error first and answer their documented 500; status paths keep their
mapping; presence flags use "stored text non-empty".

Key load (heartbeat → offline-op check → `(8400,2)` lock → create/re-wrap/unwrap
in one transaction) runs in `main` after the database opens and before any
cron; the key itself is parsed before the database is opened. After the
listener opens, a 30 s refresher (reload keys, install, flush
`sealed_count`, automatic rotation at 2^30, heartbeat) and the automatic
conversion (maintenance lock, `xmin` compare-and-set batches) start. A 60 s
seal lease gates every `Seal`.

`paceday-operator encryption` (status, convert, rotate-data-key,
decrypt-all, reset-lost-key, downgrade-schema) is created here because PAC-71
has not merged (contract 7.3); migration `034` creates `operator_audit` with
PAC-71's definition if missing and widens its CHECK, so either merge order
works.

## 2. Backend

### Tests (written with the code; see PR AC map)

| Test | File | AC |
|---|---|---|
| TestPAC84_SealTwiceDiffersAndHidesValue, _ValueBoundToItsPlace, _AnyByteChangedFails, _PlainFormsAndSeedClassifyPlain, _EnvelopeErrors, _AADInjective, _ParseKeyConfig, _KEKNeverFormatsKey, _KeyringFromWrappedKeys, _SealGateAndReporter, _OpenSettingsRowUnderOneMillisecond | internal/secretbox/secretbox_test.go | AC-3, 5, 6, 9, 10, 26, D-3 |
| TestPAC84_CommittedTestKeyRefused, _RefusalLines | config/encryption_test.go | AC-8, 9, 30 |
| TestPAC84_ConversionEndToEnd, _ConversionNeverOverwritesANewerWrite, _ConversionResumes, _RotateDataKey, _DecryptAllAndDowngrade, _ResetLostKey, _KeyLoad, _LocksAndOfflineOperation, _SealLease, _Refresher, _PerFieldFailure, _OperatorAuditPinned | storage/encryption_test.go | AC-1 (data), 7, 11, 11b, 12–15, 16c, 19, 19c, 20, 21b, 21c, 22, 23, 26, 27, 29, 31, 32 |
| TestPAC84_Conferencing…, _ZoomDisconnect…, _Google…, _RecapTestSend…, _SSO…, _AIKey…, _PersonalCalendarList… | api/pac84_decrypt_failure_test.go | AC-16, 16b, 16c, 19b |
| TestPAC84_UndecryptableHostIsBusy…, _PersonalBlockerSkipsOnly… | engine/pac84_test.go | AC-17, 18 |
| TestPAC84_Operator* | cmd/paceday-operator/encryption_test.go | AC-20, 21, 21b, 21c, 21d, 22, 23, 31 |
| TestPAC84_BackendKeyLoadConvertAndReport | main_pac84_test.go | AC-9, 19, 26, 5.3 |

### Migration

- Up `034_encryption_at_rest.up.sql`: `operator_audit` IF NOT EXISTS (PAC-71
  033 lines 61-74 verbatim) + 8-action CHECK; `encryption_data_keys` (+ sequence,
  one-active index); `encryption_backend_heartbeats`. Header records the
  "no SQL moves a secret" rule and the restate-all-actions rule.
- Down: `DO` refusal while anything is encrypted; drops the two tables and the
  sequence; keeps `operator_audit`. Run only by `downgrade-schema`, which checks
  in Go first and `Force`s the version back if the step fails.
- Backfill: none in SQL; the conversion encrypts plain values online.
- `migrations_down_test.go`: PAC-71's `keptByDown` hunk applied verbatim; the
  step-down loop reads the version after each step (034 may precede 033).

### Generated code

None (no HTTP change; bundle sha256 unchanged).

## 3. Frontend

None.
