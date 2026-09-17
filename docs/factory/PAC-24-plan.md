# Implementation plan — PAC-24: per-user settings, a write-only provider key, and a write that merges

- **Spec**: `docs/specs/PAC-24.md`
- **Contract**: `contracts/features/PAC-24.yaml` (revision 1, hash
  `6e2bb0fcc5ed6a57b6bc75a3b89fd4246f0afb4c98051ff8141f1206a4d316cf`),
  rationale in `contracts/features/PAC-24.md`
- **Resolves**: API-002, API-003, API-004, API-009, API-013, API-037, API-081,
  API-082, `x-uncertain` U-18, U-19, U-20
- **Does not resolve**: API-054 / T8 (batch A2), API-005 (batch B2 — must not
  ship in the same release as this, per the audit's own sequencing)

> **Written in a cloud session (factory README §8).** The Go module proxy and
> the npm registry are blocked, so nothing here was compiled or executed. The
> OpenAPI assembler and the migration-register generator are pure Python and
> were run. Every file named below was opened.

---

## 1. Approach

**The shape of the change is: delete the singleton, do not wrap it.** There are
ten SQL statements pinned to `id = 1` and twenty-two call sites that reach them
through `storage.GetSettings(db)`. The tempting move is to leave `GetSettings`
in place and give it a user-aware sibling, migrating call sites gradually. That
is what migration 018 did at the schema level in September and it is why we are
here: a half-finished migration leaves a working path to the wrong answer, and
the next person uses it. `storage.GetSettings`, `storage.SaveSettings` and
`storage.insertDefaultSettings` are deleted in this PR, so that after it there is
no way to reach the settings table without a user id. The compiler then finds the
twenty-two call sites for us, which is the only exhaustive search available in a
session that cannot run the tests.

**The merge is implemented by decoding onto the loaded record, not by a
parallel pointer struct.** `encoding/json` leaves a struct field untouched when
the corresponding key is absent from the document, so unmarshalling the request
body onto a `Settings` value already loaded from the caller's row *is* the merge,
for the forty-odd scalar properties. A fifty-field pointer mirror of
`storage.Settings` would express the same thing with fifty more places to forget
a field. Three consequences must be handled explicitly and each is called out in
§2 as its own risk: `null` is a no-op to `encoding/json` but must be a 400;
`json.Decoder.DisallowUnknownFields` is what implements the contract's
`additionalProperties: false`; and unmarshalling onto a populated struct *adds*
to an existing map rather than replacing it, which is the opposite of the
contract's whole-replace rule for `workingHours` and `lunchBreaks`.

**The migration creates rows; it does not merely tighten a constraint.** The
Linear issue says "finish migration 018's NOT NULL tightening", and taken
literally that is a one-line `ALTER`. It would be wrong on its own: §2.4 of the
spec establishes that **no production code path has ever created a per-user
settings row**, so for every user except the one 018 attributed there is nothing
to tighten around, and `focus_time.go:82` already fails for them today. The
migration therefore backfills one row per user, partitioned by sensitivity per
spec OQ-1, *then* tightens. 018 is not amended — it has been applied in
production and migrations are immutable once shipped — so this is migration 024.

---

## 2. Backend — Enach/clockwise-like

### Tests to write first

Storage and migration first, because everything above them is a consumer.

| Test | File | Proves | Spec AC |
|---|---|---|---|
| `TestGetOrCreateSettingsByUser_CreatesDefaults` | `backend/storage/settings_test.go` | a user with no row gets a complete default document | AC-2 |
| `TestGetOrCreateSettingsByUser_Idempotent` | `backend/storage/settings_test.go` | a second call returns the same row id, not a new row | AC-2 |
| `TestGetOrCreateSettingsByUser_RejectsNilUser` | `backend/storage/settings_test.go` | `uuid.Nil` is an error, never a silent fallback to the old singleton | AC-1 |
| `TestSaveSettingsForUser_LeavesOtherUsersUntouched` | `backend/storage/settings_test.go` | writing user A's row changes no column of user B's | AC-1 |
| `TestSaveSettingsForUser_WritesScheduleFieldsToOwnRow` | `backend/storage/schedule_preferences_test.go` | `working_hours`/`lunch_breaks` land on the caller's row — today `saveScheduleFields` defaults to `id = 1` whenever `s.ID == 0`, which on the write path is always (`settings.go:501-507`, `ID` is `json:"-"`) | AC-1 |
| `TestRecapSendTime_RoundTripsAsHHMM` | `backend/storage/settings_recap_time_test.go` *(new)* | the column renders `HH:MM` both ways against a real Postgres — this is the test that settles U-19, which I could only reason about | AC-16 |
| `TestMigration024_OwnerKeepsEveryValue` | `backend/storage/migrations_pac24_test.go` *(new)* | seeded "before" DB: the user owning the pre-existing row still has every value, credentials included | AC-18 |
| `TestMigration024_NonOwnerGetsPreferencesNotCredentials` | `backend/storage/migrations_pac24_test.go` *(new)* | a second user gets the working-day and focus values and empty credentials, calendar address and recap destination | AC-19 |
| `TestMigration024_AttributesOrDeletesOrphanRow` | `backend/storage/migrations_pac24_test.go` *(new)* | a `user_id IS NULL` row is attributed when the earliest user has no row, deleted when they do | — |
| `TestMigration024_Down_KeepsPerUserRows` | `backend/storage/migrations_pac24_test.go` *(new)* | the down migration loosens the constraint and deletes nothing | §7 |
| `TestMicrosoftToken_IsPerUser` | `backend/auth/microsoft_oauth_test.go` *(new)* | user B connecting Outlook does not replace user A's refresh token, and does not change A's `calendar_provider` | AC-5 |
| `TestZoomTokens_ArePerUser` | `backend/storage/personal_calendars_test.go` *(new)* | same for `SaveZoomTokens` / `ClearZoomTokens` | AC-5 |

Handlers next.

| Test | File | Proves | Spec AC |
|---|---|---|---|
| `TestGetSettings_IsolatedPerUser` | `backend/api/handlers_settings_test.go` | two users, two documents | AC-1 |
| `TestGetSettings_CreatesRowForNewUser` | `backend/api/handlers_settings_test.go` | 200 with defaults, never 404 | AC-2 |
| `TestGetSettings_NeverReturnsProviderKey` | `backend/api/handlers_settings_test.go` | no value anywhere in the body equals the stored key, under any name — asserted by walking the decoded body, not by checking one field | AC-11 |
| `TestGetSettings_ProviderKeySetFlag` | `backend/api/handlers_settings_test.go` | `llmApiKeySet` is true exactly when the stored key is non-empty | AC-12 |
| `TestPatchSettings_ChangesOnlyWhatIsSent` | `backend/api/handlers_settings_test.go` | one property in, every other column byte-identical | AC-6 |
| `TestPatchSettings_EmptyValueClears` | `backend/api/handlers_settings_test.go` | `""`, `0` and `false` are values, not absences | AC-7 |
| `TestPatchSettings_RejectsNull` | `backend/api/handlers_settings_test.go` | 400 naming the property; nothing stored | AC-8 |
| `TestPatchSettings_RejectsUnknownProperty` | `backend/api/handlers_settings_test.go` | 400 — including for a snake_case spelling of a real property, which is what makes API-013 detectable | AC-6 |
| `TestPatchSettings_ReplacesScheduleMapsWholly` | `backend/api/handlers_settings_test.go` | a weekday omitted from a supplied `workingHours.days` is removed, not retained | AC-9 |
| `TestPatchSettings_IgnoresReadOnlyProperties` | `backend/api/handlers_settings_test.go` | `llmApiKeySet` and `updatedAt` accepted and ignored | AC-10 |
| `TestPatchSettings_RoundTripPreservesProviderKey` | `backend/api/handlers_settings_test.go` | GET → PATCH the same document → key intact; same document with `llmApiKey: ""` → cleared | AC-13 |
| `TestPutSettings_BehavesIdenticallyToPatch` | `backend/api/handlers_settings_test.go` | the deprecated alias is the same code path, not a second implementation | — |
| `TestValidateSettings_CrossPropertyRules` | `backend/api/handlers_settings_validation_test.go` *(new)* | `workEnd > workStart`, `lunchEnd > lunchStart`, `focusMin <= focusMax`, `bufferMinMeetingMinutes >= 0` | AC-17 |
| `TestValidateSettings_Enumerations` | `backend/api/handlers_settings_validation_test.go` *(new)* | `timezone` resolvable, `recapSendTo`, `recapSendTime`, `calendarProvider`, `conferencingProvider` — each a 400 naming the property, not a 500 | AC-17 |
| `TestDailyRecapSettings_IsolatedPerUser` | `backend/api/handlers_daily_recap_test.go` *(new)* | GET and PATCH describe the caller | AC-1 |
| `TestDailyRecapPatch_RejectsNullAndUnknown` | `backend/api/handlers_daily_recap_test.go` *(new)* | matches PATCH /api/settings | AC-8 |
| `TestDailyRecapPatch_RejectsMalformedSendTime` | `backend/api/handlers_daily_recap_test.go` *(new)* | 400, not the 500 the Postgres `TIME` cast produces today | AC-17 |
| `TestDailyRecapPatch_TouchesOnlyRecapColumns` | `backend/api/handlers_daily_recap_test.go` *(new)* | the recap PATCH no longer rewrites all 47 columns | AC-6 |
| `TestDailyRecapPreviewAndTest_UseCallersSettings` | `backend/api/handlers_daily_recap_test.go` *(new)* | destination and section flags come from the caller | AC-1 |
| `TestAuthStatus_DescribesOnlyTheCaller` | `backend/api/handlers_auth_test.go` | user B's status carries no address, provider or connection state of user A's | AC-5 |
| `TestLLMTest_UsesSubmittedConfiguration` | `backend/api/handlers_llm_test.go` *(new)* | a body naming a different provider is what gets probed | AC-15 |
| `TestLLMTest_NoBodyUsesCallersStoredConfiguration` | `backend/api/handlers_llm_test.go` *(new)* | absent body and `{}` fall back, and fall back to the *caller's* row | AC-15 |
| `TestLLMTest_NeverEchoesKey` | `backend/api/handlers_llm_test.go` *(new)* | no status code's body contains the submitted or stored key | AC-14 |
| `TestDailyRecap_RunAll_OnlyUsersWhoEnabledIt` | `backend/engine/daily_recap_test.go` | the cross join is gone; a user who did not enable it is not returned | AC-4 |
| `TestFocusTime_RunForUser_UserWithNoPriorSettings` | `backend/engine/focus_time_test.go` | no longer `no settings row for user <id>` | AC-3 |
| `TestAutoDecline_RunForUser_UserWithNoPriorSettings` | `backend/engine/auto_decline_test.go` | the silent no-op becomes a real evaluation | AC-3 |

All handler tests use the existing `createTestUser` and `withUser` helpers
(`backend/api/handlers_teams_test.go:43-61`), which are package-level and already
usable from any `api` test file. **`openTestDB` returns one shared database for
the whole package** (`backend/api/testhelpers_test.go:14-38`) with no per-test
isolation, so every test above must create its own users with distinct emails
rather than relying on a clean table. This is the single largest source of
flakiness in this change and the reason the per-user tests are written first.

### Tests that must change, and why

Per the plan-author rule, each is either a test that was wrong or a behaviour
change the spec declared. All of these are the second.

| Test | File | Decision |
|---|---|---|
| `TestGetSettings_Defaults` | `backend/storage/settings_test.go:7` | rewrite against `GetOrCreateSettingsByUser`. It asserts the defaults, which are unchanged; only the accessor changes. |
| `TestGetSettings_Idempotent` | `backend/storage/settings_test.go:33` | rewrite. It asserts "a second read did not create a new row" — still true, now per user. Declared by spec AC-2. |
| `TestSaveSettings` | `backend/storage/settings_test.go:48` | rewrite against `SaveSettingsForUser`. Same assertions, a user id added. |
| `TestSaveSettings_Update` | `backend/storage/settings_test.go:106` | rewrite. Same. |
| `TestGetSettings` | `backend/api/handlers_settings_test.go:13` | rewrite: must now put a user in the request context. Today it passes *because* the handler ignores the caller — it is the test that would have caught API-002 and does not. |
| `TestPutSettings_Valid` | `backend/api/handlers_settings_test.go:34` | rewrite. It marshals a `storage.Settings` with four fields set and asserts the response — under merge semantics the other properties are no longer zeroed, which is the whole point. Declared by AC-6. |
| `TestPutSettings_InvalidJSON` | `backend/api/handlers_settings_test.go:63` | keep, add a user to the context. |
| `TestPutSettings_InvalidTimeFormat` | `backend/api/handlers_settings_test.go:76` | keep, add a user. Note `"9:00"` is still a 400, now via a tighter pattern. |
| `TestValidateSettings_*` (five) | `backend/api/handlers_settings_test.go:92-163` | **unchanged.** Pure functions over `storage.Settings`; the validator gains rules but loses none. |
| `TestValidateSettings_DaySpecificSchedules`, `..._InvalidDaySpecificSchedules` | `backend/api/handlers_settings_schedule_test.go:9,27` | **unchanged.** |
| `storage.SaveSettings` calls in `backend/nlp/parser_test.go:267,301,335,365` | | mechanical: add a user id. These are fixtures, not assertions about settings. |
| `storage.GetSettings` call in `backend/engine/smart_schedule_test.go:198` | | mechanical: same. |

### Files to change

| File | Change | Risk |
|---|---|---|
| `backend/storage/settings.go` | Delete `GetSettings`, `insertDefaultSettings`, `SaveSettings`. Add `GetOrCreateSettingsByUser` and `SaveSettingsForUser`, both keyed on `user_id`. Key `loadScheduleFields`/`saveScheduleFields` on `user_id` and delete their `id = 1` branches (`:478-483`, `:501-507`). Add an `LLMAPIKeySet bool` field with `json:"llmApiKeySet"` that is populated on read and never written to a column, and add `json:"-"` handling so the key is omitted from responses. | **High.** This is the file. The `SELECT` column list, the 47-column `INSERT` and the scan list must stay in step three ways; they already do not (the `INSERT` omits `working_hours`/`lunch_breaks` deliberately, which is easy to "fix" and break). |
| `backend/storage/settings.go` | Render `recap_send_time` as `HH:MM` instead of `::TEXT` on both read paths (`:224`, `:439`). | Medium — resolves U-19 but changes a value existing clients parse. No consumer found (§ consumers, below). |
| `backend/api/handlers_settings.go` | `getSettings` and a new `patchSettings` on the caller's row; `putSettings` becomes a thin delegation to `patchSettings` so there is one implementation. Merge by decoding onto the loaded record, with `DisallowUnknownFields`, an explicit null pre-pass, and explicit clearing of `WorkingHours`/`LunchBreaks` before decode when their keys are present. Never emit the key. | **High.** The three merge caveats in §1 are each a silent-wrong-answer if missed, and only the tests catch them. |
| `backend/api/handlers_settings.go` | `validateSettings`: add `workEnd > workStart`, `lunchEnd > lunchStart`, `focusMin <= focusMax`, `bufferMinMeetingMinutes >= 0`, `time.LoadLocation(timezone)`, `recapSendTo`, `recapSendTime`, `calendarProvider`, `conferencingProvider`. | Low. Additive, pure, and the existing seven validator tests guard the rules already there. |
| `backend/api/handlers_daily_recap.go` | All four handlers to the caller's row. The PATCH writes only the seven recap columns instead of calling the full-row save (`:85`). Reject `null` and unknown properties. Validate `send_time`. | Medium. |
| `backend/api/handlers_auth.go` | `status` (`:82-119`) to the caller's row; the outlook branch to a per-user Microsoft token. | Medium — resolves API-009. Response shape unchanged. |
| `backend/api/handlers_llm.go` | Read the optional `LLMTestRequest` body; overlay it on the caller's stored settings; probe that. Ensure no error path echoes a key. | Medium — resolves API-037. |
| `backend/auth/microsoft_oauth.go` | `SaveMicrosoftToken` and `LoadMicrosoftToken` (`:47`, `:61`) take a `userID` and key on `user_id`. Stop writing `calendar_provider = 'outlook'` for the whole table. | **High.** Five call sites (`handlers_auth.go:98`, `handlers_conferencing.go:40,138,168`, `engine/freebusy_service.go:123`) and it is a live credential. Spec §2.5. |
| `backend/storage/personal_calendars.go`, `backend/storage/conferencing.go` | `SaveZoomTokens` (`:154`) and `ClearZoomTokens` (`:5`) take a `userID`. | Medium. |
| `backend/engine/daily_recap.go` | `RunAll` (`:122-142`): correlate the join on `st.user_id = u.id` instead of `st.id = 1`. `RunForUser` (`:72`) to the caller's row. | **High.** The current statement sends a Slack message to every user; getting the correlation wrong sends none, or sends duplicates. |
| `backend/engine/analytics.go:58`, `compression.go:46`, `freebusy_service.go:75`, `habits.go:98`, `smart_schedule.go:53,286`, `nlp/parser.go:173` | Mechanical: take the user id already in context (or already a parameter) and call the per-user accessor. | Medium in aggregate. Each is one line; there are seven of them and each needs a user id that must be confirmed to be in scope. |
| `backend/engine/personal_blocker.go:97` | Same, and the identity is available two ways: `SyncAllForUser` puts it in the context at `:51` (`auth.UserIDKey`) and `storage.PersonalCalendar.UserID` (`personal_calendars.go:13`) carries it on the record `syncOne` already holds. Prefer the record — the HTTP `Sync(ctx, calID)` path (`:75-81`) does not set the context value. | Low. Checked: the cron already iterates users (`backend/main.go:60-75`), so this is not the hidden loop-over-users it looks like. |
| `backend/api/handlers_conferencing.go:130,161,321`, `handlers_meeting_briefs.go:73`, `handlers_microsoft_auth.go:61` | Mechanical: `userIDFromCtx(r.Context())` is already available at each. | Low. |
| `backend/api/routes.go:73-76` | Register `PATCH /api/settings` beside the existing GET and PUT. | Low. |
| `contracts/openapi/MIGRATION.md` | Already regenerated at stage 2; the new `patchSettings` row is present at `:154`. | — |

### Migration

- **Up**: `backend/storage/migrations/024_settings_per_user.up.sql`
  1. `SELECT setval` on the `settings_id_seq` to `max(id)` — the legacy
     `INSERT INTO settings (id) VALUES (1)` bypassed the sequence
     (`settings.go:262`), so the sequence and the table can disagree about which
     ids are free and step 3's inserts would collide.
  2. Attribute the unattributed row, if any: if a `user_id IS NULL` row exists
     and the earliest-created user has no row of their own, give it to them;
     otherwise delete it. Migration 018 did this with a blanket `UPDATE` that
     would set two null rows to the same id and then fail on its own unique
     constraint two statements later (spec §2.2) — do not repeat that shape.
  3. Delete any remaining `user_id IS NULL` rows. A settings row belonging to no
     user is unreachable by every query in the codebase after this PR.
  4. Insert one row per user in `users` with no settings row, copying the
     **preference** columns from the row identified in step 2 (the anchor) and
     leaving every **credential, identity and destination** column at its
     column default. The exact partition is spec §8 **OQ-1 and is not yet
     confirmed** — see §7 below. If no anchor row exists, every column takes its
     default.
  5. `ALTER TABLE settings ALTER COLUMN user_id SET NOT NULL`.
  6. Leave `settings.id` alone. Nothing may renumber or move the anchor row:
     rollback depends on `WHERE id = 1` still finding the same user's row.

- **Down**: `backend/storage/migrations/024_settings_per_user.down.sql`
  `ALTER TABLE settings ALTER COLUMN user_id DROP NOT NULL`. **And nothing
  else.**

  **The down migration does not restore the prior state, and it must not try.**
  It deliberately leaves every row step 4 created. Those rows hold every setting
  every user has saved since the upgrade; deleting them is the one irreversible
  act available in this change. After the down, the reverted code reads
  `WHERE id = 1` again, finds the anchor row unchanged, and the other rows are
  inert but intact — so re-applying the migration picks them straight back up.
  What the down cannot restore is the *absence* of those rows, and nothing
  depends on that.

  Two things the down does not undo and which must be in the release notes:
  the reverted code resumes zero-value full replaces against the anchor row, so
  API-004 reopens immediately and starts destroying that user's configuration;
  and provider keys entered after the upgrade sit in rows nothing reads and
  cannot be retrieved through the API to re-enter, because write-only means
  write-only. Prefer fixing forward.

- **Backfill**: step 4 above. It is the substance of the migration, not an
  afterthought — see spec §2.4: there is no production code path that has ever
  created a per-user settings row, so without step 4 the `NOT NULL` in step 5
  would be a constraint over a table with one row and `n-1` users locked out.

- **Applied to a backed-up database only.** The audit sequences this as batch B3
  and says explicitly that it and B2 (the `audit_log` migration) must not land in
  the same release. Honour that.

### Generated code

**None of these operations move from `handwritten` to `generated` in this PR,
and that is a deliberate deviation from factory README §4's "new and modified
endpoints use the generated server interface".** The reason: `backend/api/gen/`
contains only a `README.md` — no operation in the repo has been generated yet, so
moving these nine would mean standing up `oapi-codegen`, the
`StrictServerInterface` wiring and the generated router for the first time, in
the same PR as a critical data-loss fix and a migration. That makes the revert
dangerous and the review impossible, which is the outcome §4's own rationale
exists to prevent.

The deviation must be recorded in the PR body rather than assumed, and the
follow-up is: generate `getSettings`/`patchSettings` first, as the smallest
self-contained pair, once this has shipped. If the reviewer rejects the
deviation, the correct response is to split this into two PRs — generator
plumbing, then PAC-24 — not to bundle them.

`contracts/openapi/MIGRATION.md` has been regenerated and all 120 rows read
`handwritten`, so the gate's "the generated count never goes down" rule is
satisfied trivially.

---

## 3. Frontend — Enach/smart-calendar-flow

**This PR cannot open until the backend PR has merged and the contract bundle
has been published**, per factory README §1. It then starts by regenerating
`src/api/generated/types.ts` and `src/api/generated/schemas.ts` from the merged
bundle — both files are currently absent (the directory holds only its README),
so this is the first generation, and `make openapi` needs `../clockwise-like`
checked out at the merge commit.

### Tests to write first

| Test | File | Proves | Spec AC |
|---|---|---|---|
| `normalizeSettings maps every camelCase property the contract declares` | `src/api/settingsAdapter.test.ts` | the 4-of-50 bridge is gone; driven off the generated type so a new contract property fails the test rather than being silently dropped | AC-20 |
| `normalizeSettings does not invent snake_case keys the server never sends` | `src/api/settingsAdapter.test.ts` | the current fixture is hand-built in a shape the server does not produce (`:13-20`), which is why the suite passes over a broken adapter | AC-20 |
| `settingsRequestBody emits only properties SettingsUpdate permits` | `src/api/settingsAdapter.test.ts` | `additionalProperties: false` now makes a stray snake_case key a 400 | AC-20 |
| `settingsRequestBody omits llmApiKey when the user did not touch it` | `src/api/settingsAdapter.test.ts` | round-trip preserves the stored key | AC-13 |
| `settingsRequestBody sends an empty llmApiKey only on an explicit clear` | `src/api/settingsAdapter.test.ts` | clearing is deliberate, never incidental | AC-7, AC-13 |
| `the settings page shows "key configured" from llmApiKeySet` | `src/pages/Settings.test.tsx` *(new)* | the key input renders as "a key is on file / replace it", never as a value | AC-12 |
| `llmTest posts an LLMTestRequest, not the whole settings document` | `src/api/settingsAdapter.test.ts` | `client.ts:1141-1143` currently posts the entire edited object | AC-15 |

### Files to change

| File | Change | Risk |
|---|---|---|
| `src/api/generated/types.ts`, `src/api/generated/schemas.ts` | *(new — generated, never hand-edited)* | Low, but this is the first generation in the repo and the generators have never been run against this bundle. |
| `src/api/types.ts:49-88` | Replace the hand-written 34-property snake_case `Settings` with a re-export of the generated response type, or a thin alias over it. The three properties with no server counterpart (`calendar_id`, `default_conference_provider`, `teams_enabled`) are either dropped or moved to a clearly-local type — they are not settings. | **High.** Every consumer of `Settings` in the app typechecks against this. |
| `src/api/client.ts:526-563` | `normalizeSettings` and `settingsRequestBody` become a complete mapping, or are deleted entirely if the app moves to camelCase. Deleting them is the smaller change and the one I would expect; keeping a 50-property hand-written bridge recreates API-013 in a new place. | **High.** |
| `src/api/client.ts:68-115` (`DEFAULT_SETTINGS`) and `:142` (`mockState.settings`) | Must match the generated shape or the mock fallback diverges from the real one — which is the mechanism that hid this defect. | Medium. |
| `src/api/client.ts:808-825` | `GET` unchanged; the write moves from `PUT` to `PATCH`. | Low. |
| `src/api/client.ts:1141-1161` | `llmTest` sends an `LLMTestRequest`, not the settings document. The mock branch's `need(...)` checks move to the same property names. | Low. |
| `src/pages/Settings.tsx` | Every `draft.<snake_case>` read (`:138,159,195,293,325,712`, and the rest) follows the type change. The provider-key input becomes a write-only control: it shows configured/not-configured from `llmApiKeySet`, an empty box to replace, and an explicit clear. | **High.** 857 lines and the property rename touches most of them. |
| `src/hooks/useSettings.ts:16` | `useUpdateSettings` calls the PATCH path. | Low. |

**The frontend change is not optional and is not deferrable.** With
`additionalProperties: false` on `SettingsUpdate`, the shipped frontend's write
becomes a 400 rather than a silent discard. It is already broken — those writes
are discarded today (spec §2.7) — but it fails loudly from the moment the
backend merges, and the deprecated PUT alias does not help, because the alias
shares the body schema.

---

## 4. E2E

**New**: `e2e/tests/settings.spec.ts`. It covers AC-20 (the page shows stored
values and an edit survives a reload) and, at the API level through the `api`
fixture, AC-1, AC-11 and AC-12.

**It is not blocked by the calendar seam (`e2e/SEAM-REQUIRED.md`).** Settings
need no external calendar: the seed already creates four users with per-user
settings rows (`e2e/seed/seed.sql:79-90`, already using `ON CONFLICT (user_id)`),
and `e2e/auth.ts` can mint a session for each, so a two-user isolation scenario
is directly writable — this is one of the few valuable journeys the suite can
cover today.

**It is exposed to the selector problem.** `src/pages/Settings.tsx` contains
**zero** `data-testid` attributes, and the settings page is not among the five
areas listed in `e2e/TESTIDS-REQUIRED.md`. A sixth section should be added there
naming what this spec needs: the provider-key input and its configured/not-
configured indicator, the working-hours editor, and the save button. Until then
the UI half of AC-20 depends on label and role selectors, which is workable but
brittle.

**Extend, do not duplicate**: `e2e/tests/auth-session.spec.ts` already exercises
the session boundary; AC-5 (auth status describes only the caller) belongs there
as an additional case rather than in a new file.

---

## 5. Sequencing

1. **Backend, contract stage** — *done*. Fragments edited, bundle and migration
   register regenerated, `openapi_assemble.py --check` green. Awaiting
   `contract-challenger`.
2. **Answer spec OQ-1** (the migration partition) and **OQ-3** (do the repos
   deploy together). OQ-1 blocks step 4; OQ-3 decides whether the deprecated PUT
   alias merges at all. Neither is a code question.
3. **File the two follow-up issues** named in `contracts/features/PAC-24.yaml`
   (`retirePutSettings`, `calendarCredentialLifecycle`) and add the three
   unregistered defects to `docs/factory/api-audit.md`. Do this *before* merge —
   a deprecation with no tracking issue is permanent.
4. **Backend PR**, tests first in the order of §2: storage → migration →
   handlers → engines. `make verify` green, output in the body. **Merge point 1.**
5. **Apply migration 024 to a backed-up database**, on its own, not in the same
   release as the `audit_log` migration (batch B2).
6. **Regenerate the frontend's types** from the merged bundle. **Merge point 2**
   (the generated files land in the frontend repo).
7. **Frontend PR**. `make verify` green.
8. **E2E**: `settings.spec.ts` and the AC-5 case in `auth-session.spec.ts`, run
   against the full compose stack once both repos have merged.

Steps 4 and 7 cannot overlap. Step 8 cannot start before 7.

---

## 6. Rollback

```
# Application code, both repos
git revert <frontend PR merge commit>     # first — it is the newer consumer
git revert <backend PR merge commit>

# Database, from the backend repo
migrate -path backend/storage/migrations -database "$DATABASE_URL" down 1
# -> runs 024_settings_per_user.down.sql: ALTER COLUMN user_id DROP NOT NULL
```

**The migration is not reversible without loss in one direction only, and it is
the direction nobody expects.** Reverting *loses nothing* — the down migration
deletes no rows, so every per-user settings row written since the upgrade
survives intact and re-applying the migration picks them up again. What the
revert cannot undo is what the reverted *code* then starts doing:

- The reverted handlers write `WHERE id = 1` again with zero-value full
  replaces, so the anchor user's configuration starts being destroyed on the
  first partial save (API-004 reopens).
- Provider keys entered after the upgrade are in rows the reverted code does not
  read, and cannot be retrieved through the API to re-enter, because they are
  write-only. Recovering them needs database access.
- The shared provider key is readable again by every account (API-003 reopens).

So: take a database backup before step 5 and before any revert, revert the
frontend first, and treat a revert as reopening three critical findings rather
than as a neutral undo. Fixing forward is preferable in almost every case.

---

## 7. What I could not determine

- **The migration's copy partition (spec OQ-1) is not settled**, and step 4 of
  the migration cannot be written until it is. My proposal is in the spec's OQ-1
  table: preferences copied, credentials and identity and destinations not. Two
  rows are genuinely arguable — copying the provider *selection* without its key
  leaves every user pointed at a provider they cannot reach, and the timezone is
  a preference in a distributed team and a deployment default in a single-region
  one. **Cheapest way to settle it**: ask how many real users the production
  deployment has. If the answer is one, the partition is academic and the
  migration should copy nothing to nobody; every option is equivalent.
- **Whether `oapi-codegen` and `openapi-typescript` honour `readOnly` and
  `writeOnly`** the way the contract depends on (`contracts/features/PAC-24.md`
  §2, §8.1). If they do not, the generated response type carries `llmApiKey` and
  the contract has not prevented the leak it was written to prevent. **Cheapest
  way to settle it**: run each generator against the merged bundle and grep the
  output — a minute's work in a session with a package registry, which this was
  not. This is the highest-value unknown in the plan.
- **The exact rendered form of `recap_send_time`** (U-19). I reasoned from
  Postgres's documented output for `time`, not from an observed response. The
  plan sidesteps it by rendering to `HH:MM` explicitly, and
  `TestRecapSendTime_RoundTripsAsHHMM` is the proof. **Cheapest way**: that test.
- **Whether moving the Outlook credential per-user is sufficient** for the
  connection to actually work per user (spec OQ-6) — refresh, revocation, and
  the conferencing status all read it, and I could not establish by reading
  whether they are correct once it is no longer shared. **Cheapest way**: write
  `TestMicrosoftToken_IsPerUser` first and see what else fails.
- **Whether any consumer outside the two repositories** parses
  `recap_send_time` as `HH:MM:SS`, or reads `llmApiKey`. I searched both repos
  and the MCP server; the MCP server's `clockwise_get_settings`
  (`mcp/tools.go:233`) passes the JSON through untyped and is unaffected. I
  cannot see operator scripts. **Cheapest way**: ask whoever runs the deployment.
- **Which of the seven remaining engine and parser call sites do *not* have a
  user id in scope.** I confirmed `personal_blocker.go:97` does (see the table)
  and did not trace the other six to their entry points. Any one of them that
  turns out to be called from a place with no identity is a bigger change than
  "one line" — it becomes a loop over users, or a signature change that
  propagates. **Cheapest way**: delete `storage.GetSettings` first and read the
  compiler's list; it is exhaustive and takes one build. That is the first thing
  to do at stage 4 and the reason §1 chooses deletion over deprecation.
