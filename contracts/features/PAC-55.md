# Contract — PAC-55: a per-user 12/24-hour time format

- **Spec**: `docs/specs/PAC-55.md` (challenged; OQ-1 to OQ-5 closed)
- **Manifest**: `contracts/features/PAC-55.yaml`
- **Builds on**: PAC-24 (per-user settings, PATCH merge, the shared
  `&settingsProperties` map), merged as Enach/clockwise-like#204.

## 1. The change

One property on the settings document:

```yaml
timeFormat:
  type: string
  enum: ['12h', '24h']
```

It is added to the shared property map, so `Settings` (GET, PATCH response)
and `SettingsUpdate` (PATCH body) both carry it, and it is in `Settings`'
required list: a GET always returns it. Nothing else in the contract changes.

## 2. Why this shape

- **One setting, two values, no "unset" (spec §3, owner's "go for
  simplicity").** An enum of exactly the two choices. `""` is not in it: the
  spec says changing the preference means choosing the other value, so an
  empty value is a 400 like any other value outside the set (spec AC-5), the
  same rule PAC-24 applied to the other settings with no empty state.
- **The default is a column default, not a client convention.** The column is
  `NOT NULL DEFAULT '12h'`, so every existing user and every new row reads
  `12h` — exactly today's display — and the client never has to interpret an
  absent value. The spec's "until the choice is known, show 12-hour" is a
  client loading rule, not a wire state.
- **Names.** `timeFormat` follows the camelCase of every other settings
  property. The values `12h`/`24h` are short, unambiguous and stable; a
  boolean (`use24Hour`) was rejected because a third format would need a new
  property, and because a boolean has no natural "default" reading in a
  document where `false` is also a written value.
- **Display only.** The server formats nothing with it. The Slack recap and the
  booking confirmation email already use 24-hour times composed by the server
  (spec §5) and are out of scope; the public booking page never reads it.
- **Merge semantics come from PAC-24.** Absent means unchanged (AC-4), present
  means set exactly (AC-2), `null` is a 400 (AC-5), and only a changed value is
  validated — a stored value is always one of the two because of the CHECK
  constraint.

## 3. What a challenger should attack

- Whether any consumer breaks on the added property. (Revision 1 said the web
  must regenerate before the server; that was wrong — see Revision 2.)
- Whether `""` being a 400 contradicts spec AC-5 or AC-7 of PAC-24 (it should
  match both).
- Whether the e2e criteria (AC-8, AC-10, AC-11) can be written against the
  surfaces as they render today.

## Challenge (revision 1) — 2026-10-15

*contract-challenger. I did not write this contract and did not ask the author
for their reasoning. Reviewed the committed state of `feat/pac-55-time-format`
at f8b471d (`git show` / `git archive`, not the working tree) against
`docs/specs/PAC-55.md` at that commit, the PAC-24 handler and storage it builds
on, and `smart-calendar-flow` `main` (ed44af0). Not posted to Linear (no Linear
tool in this session); this section is the record.*

**Verdict**: accept-with-changes

The shape is right: one closed enum on the shared `&settingsProperties` map,
required in `Settings`, so a GET can only ever return `"12h"` or `"24h"`, and the
PAC-24 merge machinery picks the property up with no new code path (the
accepted-property set is reflected from the struct's json tags,
`backend/api/handlers_settings.go:55-65`). One sentence of the property's
description promises a message the server will not send (blocking 1). The other
findings are wording, manifest completeness and a red generated-code gate.

### Commands run, and what they printed

- `py -3 scripts/openapi_assemble.py --check` on a `git archive f8b471d` copy:
  `OK: bundle is in sync (98 paths)`, exit 0.
- `py -3 scripts/openapi_migration_report.py --check` (same copy):
  `OK: MIGRATION.md covers all 119 operations (0 generated)`, exit 0.
- `bash scripts/openapi_gen_go.sh --check` (same copy, oapi-codegen on PATH):
  **exit 1**, 68 diff lines. `backend/api/gen/paceday.gen.go` at f8b471d has no
  `TimeFormat`; a fresh generation adds `SettingsTimeFormat`,
  `SettingsUpdateTimeFormat` and the two fields (non-blocking 1).
- `git show f8b471d:contracts/openapi/openapi.yaml | sha256sum` →
  `65afee5c15904b960b9dde639e41961f936e915a5f924367e0b23c2b98ab7d29`. This
  matches `contractHash` in `contracts/features/PAC-55.yaml:6`. The bundle is
  byte-identical at 6237b9c.
- A throwaway Go program (scratchpad, not in the repo) decoding into a struct
  with ``TimeFormat string `json:"timeFormat"` `` the same way the handler does
  (`json.Unmarshal(raw, s)`, `handlers_settings.go:117`):
  `{"timeFormat":24}` → `json: cannot unmarshal number into Go struct field
  Settings.timeFormat of type string`; `true` and `["24h"]` give the same error
  for bool and array. `""` decodes without error and is left for validation.
- Not run: the web repo's `make openapi-check`, `make verify` and e2e. By
  inspection, `src/api/generated` on `main` contains no `timeFormat`, which is
  expected before the contract merges (factory §1).

### Blocking

1. **The `timeFormat` description promises a message the server's decode step
   will not send.** `contracts/openapi/paths/scheduling.yaml:1920-1921` says
   "`""` and any other value are a 400 `timeFormat must be 12h or 24h`". A
   non-string is "any other value":

   ```
   PATCH /api/settings
   {"timeFormat": 24}
   ```

   The PAC-24 handler decodes the body onto `storage.Settings` before it
   validates anything (`handlers_settings.go:117-119`). The decode fails, so the
   answer is
   `400 {"error":"invalid JSON: json: cannot unmarshal number into Go struct field Settings.timeFormat of type string"}`,
   not the catalogued message. `true`, `["24h"]` and `{}` behave the same way.
   The status and the error shape do match. Spec AC-5 still holds, because the
   error names `timeFormat`. But a contract test transcribed from this sentence
   fails against a correct implementation, and making the sentence true would
   need a type pre-check that no other settings property has. *Fix*: at
   `scheduling.yaml:1920-1921`, change "any other value" to "any other
   **string**", and add that a non-string is a 400 `invalid JSON: <err>`, as the
   `SettingsValidationError` catalogue already lists (`scheduling.yaml:805`).
   Then re-run `make openapi`. The same wording applies to the catalogue line at
   `scheduling.yaml:840`.

### Non-blocking

1. **`make openapi-check` is red at this commit** (Go generated code drift, see
   above). The stage-2 gate requires generated code in sync with the bundle
   (factory §2, gate 4). Before the contract is approved, run `make openapi`,
   commit `backend/api/gen/paceday.gen.go`, and re-record the hash if the bundle
   changes. A naming side effect of that regeneration: oapi-codegen names the
   constants `SettingsTimeFormatN12h` / `SettingsTimeFormatN24h` (and
   `SettingsUpdateTimeFormatN12h` / `SettingsUpdateTimeFormatN24h`). The
   leading-digit values force the `N` prefix. This is acceptable, but if the
   backend will reference them, pin readable names now with `x-enum-varnames`
   (for example `TimeFormat12h`, `TimeFormat24h`) rather than in a later
   generated-code diff.

2. **The rationale states the wrong consumer risk and the wrong deploy
   direction** (`contracts/features/PAC-55.md:49-51`). It says "the web's
   generated zod `Settings` is strict: the web must regenerate before or with
   the server". Two corrections:
   - No runtime code parses the strict zod `Settings`
     (`smart-calendar-flow/src/api/generated/schemas.ts:392`). The only import
     from `generated/` is `SettingsUpdate` at `src/api/client.ts:2`, used for
     `.shape` keys at `:636-638`. GET responses pass through the untyped
     `normalizeSettings` spread (`client.ts:578-605`).
   - **Old web + new server is safe.** A GET carrying `"timeFormat":"24h"` is
     spread through untouched. On save, `settingsPatchBody` drops every key not
     in the old `SettingsUpdate.shape` (`client.ts:646-656`), so `timeFormat` is
     never sent.
   - **New web + old (PAC-24-only) server breaks saves.** The new web would send
     `PATCH /api/settings {"timeFormat":"24h"}` and get
     `400 {"error":"unknown property: timeFormat"}`
     (`handlers_settings.go:88-91`). If the web defaults a missing preference to
     `"12h"` in its draft, `prev` lacks the key, so
     `JSON.stringify(undefined) !== '"12h"'` (`client.ts:652`) and **every**
     Settings save sends it and 400s.

   So: deploy the server before or with the implementing web, and revert the web
   before or with the server (spec §7). Regenerating the types alone, ahead of
   the server, is harmless: nothing populates the key until the web implements
   it. *Fix*: correct §3 of this document, and record the order in the
   manifest (next item).

3. **Manifest is missing required fields** (`.claude/agents/contract-author.md`,
   "The feature manifest"). `gates` and `rollback` are absent from
   `contracts/features/PAC-55.yaml`. The down-migration note sits under
   `migration.down` instead. Add a `rollback` that carries non-blocking 2's
   order, and a `gates` block saying what was and was not run. Also add
   `mergeConditions`, as PAC-24 does: this contract's precondition (spec header
   and OQ-4: the prerequisites "claimed … in a merged contract") rests on
   Enach/clockwise-like#206 and Enach/smart-calendar-flow#101. Both were
   **OPEN, unmerged** on 2026-10-15 (`gh pr view`). At f8b471d the spec still
   lists OQ-2/OQ-4 as open, while this document's header says "OQ-1 to OQ-5
   closed" (`PAC-55.md:3`). That is true only once #206 merges.

4. **The touch-it-fix-it bookkeeping is silent.** `resolvesFindings: []` is
   correct, but `doesNotResolve` should name the two open findings on these
   operations and their owners, so the gate reader does not have to rediscover
   them:
   - API-025 (`docs/factory/api-audit.md:259`, still "Reported" at f8b471d) is
     fixed in code by PAC-24's exact-key check (`handlers_settings.go:285-313`).
     I verified this by reading the code.
     `"workingHours":{"mode":"by_day","days":{"Monday":{...}}}` is now
     `400 workingHours.days.Monday is not a valid weekday`.
   - API-039 (`api-audit.md:273`) is web-only and is claimed by
     smart-calendar-flow#101, which is unmerged.

5. **Acceptance tests are narrowed in transcription**
   (`contracts/features/PAC-55.yaml:67-68`):
   - Spec AC-10 has two halves: "without reloading the calendar's hour axis is
     in 24-hour form" and persistence after a storage clear. The manifest keeps
     only "persistence across a fresh session". Restore the same-session half.
   - Spec AC-6's fixture times (00:05, 09:00, 13:30, 23:59) and its "12-hour
     output identical to before" guard are reduced to "both preferences".
     Transcribe them.

   AC-1 to AC-5, AC-7, AC-8, AC-9 and AC-11 map correctly. For AC-5, consider
   adding a non-string such as `24` to the payload list, with the
   `invalid JSON` message from blocking 1.

6. **The `SettingsUpdate` description's list of properties whose enum or pattern
   excludes `""`** (`scheduling.yaml:1993-1996`: `recapSendTime`,
   `recapSendTo`, `calendarProvider`, `conferencingProvider`) does not include
   `timeFormat`. The general rule covers it, but the list now reads as
   exhaustive and is incomplete. Add `timeFormat`.

7. **The e2e criteria are writable today, with the caveats below for the
   e2e-author.** None of them blocks the contract.
   - **AC-8.** The week grid renders for a user with no calendar connected
     (`smart-calendar-flow/src/pages/Dashboard.tsx:442-471`; e2e
     `calendar.spec.ts:61-62` already asserts the chrome). The axis comes from
     `slotDuration="00:30:00"`, `slotMinTime="07:00:00"` and
     `slotMaxTime="21:00:00"` (`Dashboard.tsx:480-482`). FullCalendar labels
     hourly slots, so today it reads "7am" … "8pm", and with the preference the
     expected axis is "07:00" … "20:00", as AC-8 says.
   - **AC-10.** A fresh browser context via `storageStateFor`
     (`e2e/auth.ts:144-155`) is the practical "storage cleared and signed in
     again". A *literal* clear also removes `onboarding_profile_selected`, which
     gates `RequireAuth` (`e2e/auth.ts:118,133`), and lands a user without
     `detected_at` in onboarding instead of the calendar.
   - **AC-11.** The bookings list is `LinkBookingsDialog`
     (`src/components/links/LinkBookingsDialog.tsx:17-23`), opened by the
     button labelled `Bookings for <title>` (`src/pages/Links.tsx:158`). Use a
     fresh host, as `e2e/tests/settings.spec.ts:16-21` does, because the suite
     runs in parallel and seeded users are shared. "The existing check passes
     unchanged" (`public-booking.spec.ts:298`) runs on `LINKS.uiBooking`, whose
     host is not the 24-hour user. The public-page half therefore needs its own
     assertion on that host's slug to guard anything.
   - **Spec drift worth fixing at the next spec touch.** Spec §2.1 still says
     the e2e stack cannot show calendar events. Since PAC-43 the Google
     stand-in exists (`e2e/tests/calendar.spec.ts:12-18`, `e2e/fake-google/`),
     so AC-9's event tile could be an e2e assertion too. §2.4 still describes
     PAC-24 as unmerged; it merged as #204 (c3d27bf).

### Requests wrongly permitted

None found beyond blocking 1, which is a message mismatch, not an admission.
Attacked:
- `{"timeFormat":""}`: enum excludes it. The decode leaves `""`, it differs from
  the stored value, it is validated, and the result is 400.
- `{"timeFormat":"24H"}`, `{"timeFormat":"24"}`, `{"timeFormat":" 24h"}`: all
  outside the enum.
- `{"timeFormat":null}`: 400 `timeFormat must not be null`
  (`handlers_settings.go:93-95`).
- `{"time_format":"24h"}`: 400 `unknown property: time_format` (`:89-91`).
- `{"timeFormat":"24h","workStart":"25:00"}`: one validation failure aborts the
  save (`:133-136`), so nothing is stored. This matches AC-5's "nothing is
  stored".
- `{"timeFormat":"12h"}` when the stored value is `"12h"`: unchanged, not
  validated, 200. Harmless.
- A GET document submitted back: `timeFormat` unchanged, accepted, as
  `SettingsUpdate` promises.

### Responses the client cannot handle

None. `timeFormat` is required and is a closed two-value enum. The column
default plus `INSERT INTO settings (user_id)` (`backend/storage/settings.go:242`)
and the seed's explicit-column insert (`e2e/seed/seed.sql:105`) mean every row
has a value. `getSettings` and `patchSettings` are the only operations that
return `Settings` (bundle `openapi.yaml:4414,4454`).

### Consumers affected

- smart-calendar-flow `src/api/client.ts:2,578-656`: no break against the new
  server. It breaks only if the implementing web ships ahead of the server
  (non-blocking 2).
- smart-calendar-flow `src/api/generated/schemas.ts:392,395`: regenerated,
  strict, not parsed at runtime.
- mcp `mcp/tools.go:233-236` (`clockwise_get_settings`): passes the JSON
  through. Unaffected.
- e2e `e2e/tests/settings.spec.ts`: uses `toMatchObject`, with no exact-key
  assertion. The submit-back test re-sends `timeFormat` unchanged and is
  accepted. Unaffected.
- backend `handlers_llm.go:23-40` (`DisallowUnknownFields` on its own
  three-field struct): unaffected.

### Contract/implementation divergences

1. Blocking 1. Apart from that, the contract matches what PAC-24's handler will
   do once a ``TimeFormat string `json:"timeFormat"` `` field, the column and the
   validator line exist. Unknown-property rejection, null rejection,
   changed-only validation (`handlers_settings.go:128-136,190-202`) and the
   fresh re-read response (`:141-147`) all extend to the new property with no
   new mechanism. `SaveSettingsForUser` lists its columns explicitly
   (`storage/settings.go:284-368`). If the implementation omits `time_format`
   there, a PATCH would answer 200 with `"12h"`. AC-2 catches that, so it is a
   plan item, not a contract defect.

### Uncertainties not actually resolved

None claimed (`resolvesUncertain: []`). The settings operations carry no
`x-uncertain` (spec header, checked against the fragment).

### Verified clean

- The shared anchor: `timeFormat` appears in `Settings` and, through
  `<<: *settingsProperties`, in `SettingsUpdate`. The bundle aliases it as
  `*id050` in both. It is required only in `Settings`, and
  `additionalProperties: false` is unchanged on both.
- Naming: camelCase like every other settings property. The values are short
  and stable. The rejected boolean alternative is argued.
- Security: no new operation, no `security` change, nothing reaches the public
  booking response.
- `contractHash` reproduces. The assembler and the migration register are in
  sync.
- Spec AC-1 to AC-5 are expressible at the boundary, and each has a manifest
  test.

---

## Revision 2 — 2026-10-15

*contract-author, responding to "Challenge (revision 1) — 2026-10-15" (verdict
**accept-with-changes**, one blocking finding).*

**Blocking 1 — "any other value" promised a message the decoder pre-empts.
Accepted.** The description now says `""` and any other *string* get
`timeFormat must be 12h or 24h`, and a non-string is the existing
`invalid JSON: <err>` naming `timeFormat`; the catalogue line says the same.
The backend test asserts both (`{"timeFormat":24}` is a 400 with an error).

**Non-blocking.**
1. Generated Go code regenerated and committed with this revision. The enum
   constants keep oapi-codegen's `SettingsTimeFormatN12h` names: the backend does
   not reference them, and `x-enum-varnames` on the shared map would give
   `Settings` and `SettingsUpdate` the same constant names.
2. Deploy order corrected, as the challenger derived it: nothing parses the
   strict zod `Settings`, and an old web never sends `timeFormat`, so the risk is
   the *new* web against an *old* server. The server deploys first or with the
   web; the web is reverted first. Recorded in the manifest's `rollback` and
   `mergeConditions`; spec §7 already says "revert the web before, or together
   with, the server".
3. Manifest gains `gates`, `rollback` and `mergeConditions` (#206 and
   smart-calendar-flow#101 merged first).
4. `doesNotResolve` names API-025 (fixed by PAC-24; register updated by #206)
   and API-039 (smart-calendar-flow#101).
5. `acceptanceTests` restore AC-6's fixture times and 12-hour regression guard
   and AC-10's "without reloading" half, and record that a fresh browser context
   stands in for cleared storage (clearing it literally drops the onboarding
   marker); AC-11's public half is asserted on the same fresh host.
6. `SettingsUpdate`'s list of properties that refuse `""` now includes
   `timeFormat`.

