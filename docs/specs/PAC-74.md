# Spec — PAC-74: Settings AI key field: clearing a typed key deletes the saved key; single-flag fallback shows the wrong provider's key

- **Linear**: https://linear.app/paceday/issue/PAC-74/settings-ai-key-field-clearing-a-typed-key-deletes-the-saved-key
- **Status**: accepted (challenge 2026-11-14, below)
- **Author**: spec-author
- **Surveyed at**: clockwise-like `origin/main` fd4e7b1, smart-calendar-flow `origin/main` 4f71ad2
- **Inherited scope**: none. `docs/factory/api-audit.md` has no open finding on the
  write-only provider key of `PATCH /api/settings` (API-002/003/004/013 are Fixed by
  PAC-24; API-039 is open but concerns `conferencingProvider`, owned elsewhere, and its
  `LLMProvider` half is Fixed). The `Settings` / `SettingsUpdate` schemas carry no
  `x-uncertain` marker (the nearest, `openapi.yaml:12809/12817`, are on another schema).

## 1. Problem

Two defects in the "API key" field of the AI section of the Settings page. Both
predate PAC-73 part 1 and were reported non-blocking by its reviewer.

1. **Data loss.** A user who already saved a key for their AI provider starts typing in
   the key field, changes their mind and erases what they typed, then saves some other
   setting. Their saved key is deleted. They never pressed "Remove saved key". Because
   keys are write-only (PAC-24, owner decision), they cannot see that it happened or get
   the key back; AI stops working and the only fix is to find the key again and re-enter
   it. The connection test misleads too: it says the provider "needs an API key".
2. **Wrong status.** When the settings the page reads carry only the single "key on file
   for the saved provider" flag and not the per-provider map (demo mode, or a server
   older than the per-provider map), switching to a different provider shows "A key is
   saved" and "Remove saved key" for a provider that has no key. The user may skip
   entering a key and save, and the new provider has no key.

## 2. Current behaviour

### 2.1 Web: the key field and what Save sends (smart-calendar-flow, `git show origin/main:`)

- The field is a password input bound to the draft's `llm_api_key`:
  `value={draft.llm_api_key ?? ""}` and `onChange={(e) => set("llm_api_key", e.target.value)}`
  (`src/pages/Settings.tsx:765-766`; the issue's "~717" is stale). `set` stores the raw
  string (`Settings.tsx:168-170`), so erasing all typed text leaves `llm_api_key === ""`.
- Placeholder: "A key is saved. Type to replace it" when `keyIsSetFor(draft, draft.llm_provider)`,
  else "Paste an API key" (`Settings.tsx:768`).
- "Remove saved key" is shown only when a key is on file **and** `draft.llm_api_key === undefined`
  (`Settings.tsx:770`); pressing it applies `removeSavedKey` (`Settings.tsx:773`), which sets
  `llm_api_key: ""`, `llm_api_key_set: false` and that provider's map entry to `false`
  (`src/lib/aiProviders.ts:238-242`).
- So in the draft, `""` means two different things that the code cannot tell apart:
  "the user pressed Remove saved key" and "the user typed and erased". `undefined` means
  untouched.
- Save calls `update.mutateAsync(draft)` (`Settings.tsx:229`) → `api.updateSettings(s, previous)`
  with `previous` = the settings last read (`src/hooks/useSettings.ts:17`) → PATCH body
  `settingsPatchBody(s, previous)` (`src/api/client.ts:1115-1116`). `settingsRequestBody`
  renames `llm_api_key` → `llmApiKey` (`client.ts:843`, `:915-921`); `settingsPatchBody`
  keeps every property whose value is not `undefined` and differs from `previous`
  (`client.ts:956-966`). The settings read never contain a key, so `llmApiKey: ""` is sent
  whenever the draft holds `""` (pinned by `src/api/settingsAdapter.test.ts:228`).
- After a successful save the draft is replaced by the server's answer (`Settings.tsx:230`),
  which has no key, so the field returns to untouched.
- The connection test body (`llmTestBody`, `client.ts:983-994`) sends `llmApiKey: ""` for
  `""` and nothing for `undefined`; the contract makes `""` mean "no key, do not use the
  stored one" (`contracts/openapi/openapi.yaml:12159-12162`). So after type-and-erase the
  test answers "needs an API key" although a key is stored.
- Switching provider (`Settings.tsx:654`) applies `providerSwitch(next, data)`
  (`aiProviders.ts:215-224`): draft key back to `undefined`, and **both** presence flags
  copied from the saved settings: `llm_api_key_set: saved?.llm_api_key_set`,
  `llm_api_keys_set: saved?.llm_api_keys_set` (`aiProviders.ts:221-222`).
- The `llmUnboundApiKey` legacy key has no text input, only a "Remove old key" button
  (`Settings.tsx:783-794`); it is not affected by defect 1.

### 2.2 Backend: PATCH semantics of the key properties (clockwise-like `origin/main`)

There is one write-only key property, `llmApiKey`, bound to the provider the row has
**after** the write; plus `llmUnboundApiKey`, which only accepts `""`.

| Body | `llmApiKey` | `llmUnboundApiKey` |
|---|---|---|
| absent | stored key unchanged (`newKey` stays nil, `backend/api/handlers_settings.go:160-168`); the stored secret is written back byte-identical (PAC-84 comment, `:202-204`) | unchanged (`:169-180`) |
| `""` | the key bound to the post-write provider is cleared: `s.LLMAPIKeys.Set(s.LLMProvider, "")` (`:195`) and the column marked replaced (`:205-208`, PAC-84 AC-16c b). If that provider is `""`/`organization`/`unsupported`, `Set` returns false and `""` is a silent no-op (`:195`) | removes the unbound key (`:177-180`, `:198-200`) |
| non-empty | replaces the key bound to the post-write provider (`:195`); 400 if that provider is not one of the five (`:195-197`) | 400 `msgLLMUnboundAPIKey` (`:174-176`, `:44`) |
| `null` | 400 "llmApiKey must not be null" (`:132-135`) | 400 (`:132-135`) |
| non-string | 400 "llmApiKey must be a string" (`:163-166`) | 400 (`:172-173`) |

The contract states the same thing for `SettingsUpdate.llmApiKey`: "omit it and the stored
key is unchanged; send `""` and it is cleared; send a value and it is replaced"
(`contracts/openapi/openapi.yaml`, `SettingsUpdate` at `:14720`, description at `:14807-14822`
the sentence "Under the merge semantics..." is at `:14816`). Pinned by
`backend/api/handlers_settings_test.go:151` (`TestPatchSettings_EmptyValueClears`), `:223`
(`TestPatchSettings_RoundTripKeepsKeyAndIgnoresReadOnly`) and
`backend/api/byok_test.go:601` (`TestBYOK_KeysAreBoundToTheirProvider`).

**The server behaves as specified. Defect 1 is the web sending `""` the user did not mean.**

### 2.3 Presence flags (`*Set`)

- `llmApiKeySet`: true exactly when a key bound to the **current** `llmProvider` is on file
  (`backend/storage/settings.go:188-190`; contract `openapi.yaml:14446-14456`).
- `llmApiKeysSet`: one boolean per key-based provider (`storage/settings.go:191-193`,
  `storage/settings_ai.go:102-110`; contract `LLMApiKeysSet` `openapi.yaml:11989`).
- `llmUnboundApiKeySet` (`storage/settings.go:194-195`).
- All three are recomputed from the stored values by `RefreshKeyFlags`
  (`storage/settings_ai.go:113-128`, called from `storage/settings.go:315`); presence means
  "stored value non-empty", even if it cannot be decrypted (PAC-84 D-3). All are read-only
  and ignored on write (`handlers_settings.go:107-109`; web never sends them,
  `client.ts:945`).
- A current server always sends the map, so defect 2 is reachable only without it.

### 2.4 The single-flag fallback (web)

`keyIsSetFor(s, provider)` (`src/lib/aiProviders.ts:226-230`): if the map is present it
answers from the map; otherwise `s.llm_provider === provider && !!s.llm_api_key_set`.
After a switch, `s.llm_provider` is the **new** provider while `llm_api_key_set` was copied
from the **saved** provider (`aiProviders.ts:221`). So with no map, saved = OpenAI with a
key, switch to Anthropic → `keyIsSetFor(draft, "anthropic")` is `true`: placeholder "A key
is saved…" and "Remove saved key" are shown for Anthropic (`Settings.tsx:768-777`). The
issue attributes this to `providerSwitch` (its "~121" is stale); the wrong answer is
produced by the two together. The existing unit test of the fallback
(`src/lib/aiProviders.test.ts:228-229`) only checks a draft whose provider is the saved one,
and the switch test (`src/api/settingsAdapter.test.ts:299-304`) only switches back to the
saved provider — neither exercises a switch to a *different* provider without the map.

Where the map is missing in practice: demo mode seeds no flags at all
(`src/demo/clientMocks.ts:50-70`, which also seeds `llm_api_key: ""`) — so in demo mode
neither flag is set and defect 2 does not show; it shows only against a server that sends
`llmApiKeySet` without `llmApiKeysSet`.

## 3. Desired behaviour

- The saved key for a provider is deleted only when the user explicitly asks for it
  ("Remove saved key"), or replaced only when they type a new key and save.
- Typing in the key field and then erasing everything typed is the same as never having
  typed: Save leaves the stored key as it is, the page still says a key is saved and still
  offers to remove it, and "Test connection" tests the stored key.
- If the user pressed "Remove saved key" and afterwards types and erases text, the
  removal they asked for still stands (the page still shows no key on file, and Save
  removes it). Erasing returns the field to the state it was in before typing — never to
  a state the user did not choose.
- When the page only knows "is a key on file for the saved provider", it applies that to
  the saved provider only. Any other provider is shown with no key on file, so the user is
  prompted to paste one. Switching back to the saved provider shows its saved state again.
- Nothing about the server changes: its write semantics are already explicit and correct.

## 4. Acceptance criteria

"Saved key" below means a key on file for the provider shown. "Erase" means any edit that
leaves the key field empty (backspacing, select-all + delete, cut).

AC-1. Given a saved key for the shown provider and the per-provider map present, when the
user types any non-empty text into the key field, erases it, changes another setting and
saves, then the save request carries no instruction about the key, and does carry the
other setting.  [unit]  — fails today (`llmApiKey: ""` is sent).

AC-2. Given a real user with a saved OpenAI key, when in the Settings page they type text
in the key field, erase it, change another setting and save, then the save succeeds, the
save request has no key property, and afterwards the server reports the OpenAI key still on
file and AI configured for OpenAI.  [e2e]  — fails today.

AC-3. Given a saved key for the shown provider, when the user types and then erases, then
the field again says a key is saved and "Remove saved key" is offered again.  [unit]
— fails today (button hidden while the field holds `""`).

AC-4. Given a saved key for the shown provider, when the user types, erases and presses
"Test connection", then the test request carries no key (so the stored key is tested), not
an empty key.  [unit]  — fails today.

AC-5. Given a saved key for the shown provider, when the user presses "Remove saved key"
and saves, then the save request asks for the key to be removed and afterwards the server
reports no key on file for that provider.  [unit] + [e2e]  — regression guard (passes
today; no e2e covers the save-after-remove path today, see §6).

AC-6. Given a saved key, when the user presses "Remove saved key", then types text and
erases it, then the page still shows no key on file and Save still asks for the key to be
removed.  [unit]  — regression guard: passes today, and fails under the fix the issue
suggests ("map an empty input back to undefined"), which would silently cancel the removal.

AC-7. Given a saved key, when the user types a new non-empty key and saves, then the save
request carries exactly that key.  [unit]  — regression guard (`settingsAdapter.test.ts:227`).

AC-8. Given settings that carry the single "key on file" flag set for OpenAI (saved
provider) and no per-provider map, when the user switches to Anthropic, then the page shows
no key on file for Anthropic: the "paste a key" placeholder and no "Remove saved key".
[unit]  — fails today.

AC-9. Given the AC-8 state, when the user switches back to OpenAI, then the page shows the
OpenAI key as saved and offers "Remove saved key"; and if they had pressed "Remove saved
key" on OpenAI before switching away and back, Save sends no key instruction.  [unit]
— regression guard (`settingsAdapter.test.ts:299-304` covers the second half).

AC-10. Given the per-provider map present (OpenAI: key, Anthropic: no key), when the user
switches between them, then each shows its own map value.  [unit]  — regression guard
(`aiProviders.test.ts:220-230`, `e2e/tests/llm-bring-your-own.spec.ts` W-1 tests).

No [contract] criterion: the contract's write semantics are not changed (D-1).

## 5. Explicitly out of scope

- **Any server or contract change.** The server's absent / `""` / value / `null` handling
  is already explicit in the contract and tested (§2.2).
- **A whitespace-only key.** Typing only spaces is a non-empty value today and is saved as
  the key; trimming or refusing it is a separate behaviour change (OQ-1).
- **An "undo removal" control.** After "Remove saved key" the only ways back are switching
  provider and back, or reloading. Unchanged.
- **The legacy unbound key** ("Remove old key"): it has no text input, so it cannot be
  hit by defect 1.
- **Demo-mode echo of the typed key** (`clientMocks.ts:439-449` stores the whole draft,
  key included). Demo data is not real; tracked by PAC-83's demo rules, not here.
- **Organisation AI.** When the provider is "organization" or the member is on the
  organisation's AI because personal keys are forbidden (PAC-73/PAC-87), no key field is
  shown (`Settings.tsx:760` renders it only for the five key providers) and the probe sends
  `{}`; nothing here changes that.

## 6. What must not change

- The key is never read back or shown (PAC-24, owner): no criterion may require the page
  to know the key's value. Protected by `TestSettings_NeverReturnsProviderKey`
  (`handlers_settings_test.go:99`).
- A key typed for one provider is dropped on a provider switch and never sent for another
  provider (contract challenge rev. 2.1 W-1). Protected by `settingsAdapter.test.ts:273-304`
  and the W-1 e2e tests (`e2e/tests/llm-bring-your-own.spec.ts:176-250`).
- "Remove saved key" followed by "Test connection" tests "no key" (`llmApiKey: ""`).
  Protected by `settingsAdapter.test.ts:244-254` and the e2e at
  `llm-bring-your-own.spec.ts:145-172`.
- A save that does not touch the key leaves the stored (encrypted, PAC-84) key
  byte-identical — server behaviour, protected by PAC-84's tests; the web change only makes
  the web omit the key more often.
- **Finding:** no e2e test drives "Remove saved key" then Save through the page and checks
  the key is gone. AC-5's e2e half adds it.

## 7. Rollback

Web-only change, no migration, no data change. Reverting the web PR restores today's
behaviour (including the data-loss path). Nothing stored by the fixed version is
unreadable by the old one.

## 8. Open questions

- **OQ-1 (owner, non-blocking):** should a key made only of whitespace be treated as
  empty (i.e. "no change") rather than saved? Default: unchanged behaviour (saved as typed),
  out of scope here. Resolve by owner reply; a follow-up ticket if "yes".

## 9. Decisions

| # | Decision | Source |
|---|---|---|
| D-1 | Web-only. No contract clarification needed: `SettingsUpdate.llmApiKey` already says absent = unchanged, `""` = cleared, value = replaced, and `null` is a 400 for every property (§2.2). | factory-assumed |
| D-2 | "Remove saved key" is the only way the page asks for a key to be removed. An empty key field never means "remove" by itself. | factory-assumed (follows the issue) |
| D-3 | Erasing typed text restores the field's intent from before typing: "leave unchanged" if no removal was pending, "remove" if the user had pressed "Remove saved key" (AC-6). This departs from the issue's literal suggestion, which would cancel a pending removal while the page says no key is on file. | factory-assumed |
| D-4 | Without the per-provider map, the single flag describes the saved provider only; every other provider is shown as having no key on file (we cannot know, and "no key" makes the user paste one rather than save a provider with none). | factory-assumed (follows the issue) |
| D-5 | After type-and-erase, "Test connection" tests the stored key, consistent with D-3. | factory-assumed |
| D-6 | Keys stay write-only; no reveal. | owner (PAC-24, 2026-10-27 LLM decision) |
| D-7 | Stored keys are encrypted at rest; an untouched key must be written back unchanged — satisfied server-side, the web only omits the key. | owner (PAC-84, 2026-11-01) |
| D-8 | Organisation AI (PAC-73/PAC-87) is untouched: no key field there. | owner (2026-10-27/28, 2026-11-01) |

## Challenge — 2026-11-14

**Verdict**: accept

Self-challenge by the same session acting as spec-challenger (role files say the
challenger should not share the author's reasoning; this was requested as one run, so the
citations below were re-checked against `origin/main` rather than taken from §2).

### Blocking
None.

### Non-blocking
1. AC-5 (unit half), AC-6, AC-7, AC-9 and AC-10 pass today. They are labelled regression
   guards; AC-1–AC-4 and AC-8 are the ones that fail today. AC-6 is the important guard:
   it fails under the fix the issue text proposes, so the implementer must not take that
   fix verbatim.
2. AC-8 can only be proven at unit level: a server built from the current contract always
   sends the map, so no e2e against docker compose can reach the fallback. Accepted.
3. The issue's line references are stale (`Settings.tsx` ~717 → 765-777; `aiProviders.ts`
   ~121 → 215-230), and it locates defect 2 in `providerSwitch` alone while the wrong
   answer comes from `keyIsSetFor`'s fallback reading a flag `providerSwitch` copied. The
   spec is implementation-neutral on where to fix it.
4. Demo mode seeds `llm_api_key: ""` (`clientMocks.ts:58`). Under D-3 the page must not
   treat that seed as a pending removal the user chose; with no flags in demo mode no
   "Remove saved key" is shown and nothing is PATCHed, so it is harmless, but the
   implementer should not rely on `""` meaning "user removed it" if they keep that
   representation.
5. Existing unit tests that pin today's representation may need rewording rather than
   deletion: `settingsAdapter.test.ts:228` (`llm_api_key: ""` → `llmApiKey: ""`) and
   `aiProviders.test.ts:117-126` (switch copies `llm_api_key_set: true`). Their *intent*
   (removal sends `""`; the map is carried over) must survive per §6.
6. OQ-1 (whitespace key) is real but separate; not needed to fix this issue.

### Criteria I could not falsify
- None. Every "fails today" AC has a test that fails on `origin/main` and passes after;
  every regression guard is falsifiable against a plausible wrong fix (AC-6 against the
  issue's fix, AC-9/AC-10 against a fix that blanks every non-saved provider even when the
  map is present).

### Citations I checked and found wrong
- None in §2. (Checked: `Settings.tsx:168-170, 229-230, 654, 765-777`; `aiProviders.ts:215-242`;
  `client.ts:843, 915-921, 945, 956-966, 983-994, 1115-1116`; `useSettings.ts:17`;
  `handlers_settings.go:107-109, 132-135, 160-180, 195-208`; `settings_ai.go:82-128`;
  `settings.go:188-195, 315`; contract `SettingsUpdate.llmApiKey`, `llmApiKeySet`.)

### Consumers this breaks
- None outside the web Settings page. `keyIsSetFor`, `providerSwitch`, `removeSavedKey`
  and the key field are used only by `src/pages/Settings.tsx` and their tests; the server
  and the MCP server do not read the web draft. The e2e suite in
  `clockwise-like/e2e/tests/llm-bring-your-own.spec.ts` keeps passing (it never types and
  erases).

### Multi-tenancy / other users
- Settings are per user (`GetOrCreateSettingsByUserCtx`, `handlers_settings.go:138`); the
  change is client-side and cannot reach another user's key.
