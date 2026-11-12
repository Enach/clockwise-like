# Contract: PAC-87 (organisation AI: re-ask only on a provider change; move forbidden members after one confirmation)

- **Linear**: PAC-87, https://linear.app/paceday/issue/PAC-87
- **Spec**: `docs/specs/PAC-87.md`, challenged accept-with-changes 2026-11-02
  (blocking 1-3 and non-blocking 1-8 applied in the body), branch
  `factory/pac-87-spec` at `cbccf33`. Amends
  `docs/specs/NEW-llm-bring-your-own-part2.md` (Part 2) and its contract
  `contracts/features/NEW-llm-bring-your-own-part2.{md,yaml}` (revision 3.1).
- **Stage**: contract-author, revision 1, 2026-11-12; contract-challenger 2026-11-12, accept-with-changes (applied in place, log at the end). Next: plan-author.
- **Manifest**: `contracts/features/PAC-87.yaml`.
- **Owner decisions applied** (`.claude/factory/owner-decisions.md`,
  2026-11-01): OQ-P2-1 re-ask only when the provider changes; OQ-P2-2 move
  members who chose their own provider onto the organisation's AI after they
  confirm once, keeping their own settings; OQ-P2-3 and OQ-P2-4 "as shipped"
  (unchanged here).

Citations are to `origin/main` `32ecacf` (clockwise-like) and
`smart-calendar-flow` `origin/main` `702a6c2`.

**What changes at the HTTP boundary: descriptions and one example only.** No
path, operation, parameter, status code, schema property, `required` list or
enum value is added, removed or changed. The fragments touched are the
organisation-AI, settings, AI-probe and parse text in
`contracts/openapi/paths/scheduling.yaml` and the brief-refresh text in
`contracts/openapi/paths/habits.yaml` (also organisation-AI prose). That is
deliberate, and §1-§2 explain why: the web validates these responses with
closed (`z.strictObject`) zod schemas, so any new property or enum value
would break every web build from before PAC-87 the moment the backend
deploys.

## 1. The consent period: how it is represented, and what a confirmation names (spec §8 item 1)

**Decision.** One number keeps both jobs it can do without conflating them:

- `configurationVersion` stays exactly as it is for administrators: it
  advances on a change of provider, any key write and any destination
  change, and an administrator's change names it (conflict check unchanged,
  P2-AC-40, PAC-87 AC-7).
- The server additionally stores, per organisation, **the version that
  started the current consent period** (`consent_since_version`, §3). It is
  set to the version produced by every change of `provider` (first setup,
  change to another provider, removal with `provider: ""`), and left alone by
  key, destination, model and `personalKeysAllowed` changes.
- **A confirmation still names the `configurationVersion` the member was
  shown** (`OrganizationAIConsentRequest` unchanged). It is accepted exactly
  when `consent_since_version <= shown <= configuration_version`, and the
  configuration is complete. Otherwise: `409 organization_not_configured`
  (incomplete; wins) or `409 configuration_changed`.
- **What is stored** for (member, organisation) is the consent period
  confirmed: the `consent_since_version` current when the confirmation was
  recorded, never the shown version. A member is "confirmed" exactly when the
  stored value equals the organisation's current `consent_since_version`,
  compared in the single statement that already reads the configuration and
  the confirmation together (`ReadOrgAIState`, `storage/org_ai.go:154-177`).

**Why this tells P -> Q -> P apart (spec §3.1 "What the server must store").**
Versions only grow (Part 2 P2-D17, kept), and every provider change produces
a new version and sets `consent_since_version` to it. If the member was shown
version V and any provider change happened after V, that change's version is
`> V`, so the current `consent_since_version > V`: refused, even if the
current provider equals the one shown (P -> Q -> P, or removal followed by a
new setup of P: two provider changes, the second one's version is the
current consent start). If no provider change happened after V, the latest
one is at or before V, so `consent_since_version <= V`: accepted, whatever
keys or destinations changed in between. No history table is needed and no
provider is compared.

(Challenge 2026-11-12: "versions only grow" also holds across deletion. The
configuration row is never deleted by the server: a removal keeps the row
and its version (`031_llm_org_ai.up.sql` (a), `org_ai.go` `Merge`), and no
statement in `backend/` deletes from `org_ai_configurations`. The only
deletion is the `ON DELETE CASCADE` from `organizations`, and
`org_ai_confirmations.org_id` cascades from the same row, so a
configuration that restarts at version 0 can only belong to an organisation
with no confirmation left to match it. Stage 4 must not add a delete path;
if one is ever needed it deletes the organisation's confirmations in the
same transaction.)

**Why the check cannot race (AC-5's per-access run).** The confirmation is
recorded by one conditional statement against the configuration row as
stored at that moment (as today, `org_ai.go:520-548`): insert/update the
confirmation with the row's `consent_since_version` only where
`consent_since_version <= V AND V <= configuration_version AND <complete>`,
under the row lock an administrator's write also takes. An administrator's
provider change either commits before (then `consent_since_version > V`,
refused) or after (then the recorded period is the old one, which the new
period does not match: the member is asked again). Stage 3 names the
statement; the property is binding.

**Exposure.** The consent period is **not** a property of
`OrganizationAIConfiguration` (see "Rejected" below). What clients need is
already on the wire: whether the caller is confirmed (`AIStatus`), the
version shown (`configurationVersion`), and, for an administrator's form,
whether the pending change alters `provider` (known to the form itself, so
its pre-save sentence of spec §3.6 / P87-D6 needs no server field). An
operator answers "when were members last asked?" from the operator record
(§5). The e2e assertion at `e2e/tests/llm-org-ai.spec.ts:119-120`, which
reads `org_ai_confirmations.configuration_version`, reads
`org_ai_confirmations.consent_since_version` instead (§3).

**Rejected.**

1. *A separate `consentPeriod` number in the read and in the confirmation
   body* (the issue's proposal). Equivalent in power, but it changes two
   schemas the web parses strictly: `OrganizationAIConfiguration` is
   `z.strictObject` in `smart-calendar-flow src/api/generated/schemas.ts:320`
   and every read goes through `.parse` (`src/api/client.ts:285-287`), so a new
   property makes every pre-PAC-87 web build fail to show the organisation-AI
   section and the consent dialog the moment the backend deploys; a new body
   property would need both repos to ship in lockstep. It would also hand
   clients a second number whose only use is to be echoed back.
2. *Confirmation names the provider shown.* Cannot refuse P -> Q -> P or a
   removal and set-up again (spec challenge, blocking 1).
3. *Keep "confirmed = stored version equals current version" and re-key every
   confirmation on each key rotation.* Would be a write to every member's
   row on each rotation, racy against a concurrent confirmation, and still
   needs the period boundary to decide which rows to carry forward.
4. *A provider-change history table.* Same answer as `consent_since_version`
   with more storage and a join on every request.

## 2. Row 10's reason, row 9's status, and what clients see (spec §8 items 2, 3)

**Row 10 answers `organization_unconfirmed` with `enablers: [user]`** (was
`personal_keys_forbidden` with `[user]`). The table on `AINotConfigured`
now reads:

| # | Stored choice | Condition | `reason` | `enablers` |
|---|---|---|---|---|
| 9 | a provider, or `unsupported` | forbids; complete; confirmed | (configured: organisation) | - |
| 10 | a provider, or `unsupported` | forbids; complete; not confirmed | `organization_unconfirmed` | user |
| 11 | a provider, or `unsupported` | forbids; not complete | `personal_keys_forbidden` | organization_admin |

Rows 1-8 unchanged; "confirmed" per §1.

Why `organization_unconfirmed`:

- **It is now row 10's meaning.** The contract already defines it as "the
  caller can turn AI on in one step by confirming (nothing is sent before)".
  Since OQ-P2-2 that is exactly row 10: one confirmation, no settings write.
  Keeping `personal_keys_forbidden` + `[user]` would keep a code whose
  documented client action ("choose Use my organisation's AI", i.e. rewrite
  the stored choice) is now wrong, so every client would change meaning
  silently.
- **Old web builds do the right thing.** `src/lib/aiProviders.ts`
  `asksConfirmation` is `reason === "organization_unconfirmed"`, and
  `AINotConfiguredNotice.tsx` then offers "Review and confirm" with
  `choose: false`: the dialog calls only `confirmOrganizationAI`. The notice
  text is the server's `message` (`noticeReason`, `aiProviders.ts:147-152`),
  which carries row 10's sentence (provider, consent sentence with
  "access", own choice kept, confirm). So a pre-PAC-87 web build, against a
  PAC-87 backend, already confirms without rewriting the stored choice.
- **No enum change.** `AINotConfigured.reason` is a closed `z.enum` in the
  web's generated schemas (`schemas.ts:8`) and `Settings.aiStatus` embeds
  it; a new value would make the settings read fail to parse for every row-10
  member on a pre-PAC-87 web build.

What a client loses: the reason alone no longer tells row 10 from row 5. A
client that needs to (the consent dialog adds "your own choice is kept",
spec §3.6) reads the caller's stored `Settings.llmProvider`: `organization`
in row 5, a provider or `unsupported` in row 10. Written into the
`AINotConfigured` description. `personal_keys_forbidden` now only ever comes
with `[organization_admin]` (row 11, and the probe refusal for naming a
provider or carrying a key while forbidden); the description says so. The
enum value stays (row 11 and the probe use it).

**Rejected**: (a) a new reason value (e.g. `personal_keys_forbidden_unconfirmed`):
breaks the strict web parse at backend-first deploy (above); (b) keeping
`personal_keys_forbidden` + `[user]` and accepting the window: a pre-PAC-87
web would keep offering the choice-rewriting action, which is exactly what
OQ-P2-2 says must not happen, for as long as the window lasts; (c) a new
`AIStatus` property `row` or `movedFromOwnProvider`: strict-parse break again,
and the stored choice already carries the information.

**Row 9's status.** `AIStatus` is `configured: true`, `configuration:
organization`, `provider` = the organisation's provider; `notConfigured`
absent. `Settings.llmProvider` stays the member's own provider (or
`unsupported`), and `llmApiKeysSet` etc. still report their kept keys. A
client tells row 9 from row 4 by `llmProvider != organization` and shows the
own choice as "kept, not used" plus "AI runs on your organisation's AI
(‹provider›)". Both `AIStatus.configured` ("rows 2, 4 and 9") and
`Settings.llmProvider` / `aiStatus` descriptions say so. The 200 of
`confirmOrganizationAI` for a row-10 caller is this row-9 status (AC-8).

Probe in row 9 (spec §3.3, AC-14), on `testLLMConnection`: `{}` probes the
organisation's configuration and never carries the stored model/key/
destination; `{}` + `llmModel` only is the existing 400 `llmModel and
llmApiKey cannot be used with your organisation's AI configuration`; naming
a provider or carrying `llmApiKey` is the existing 409
`personal_keys_forbidden` `[organization_admin]`. No new status.

## 3. Migration, rollback and either-order rule (spec §3.8, §7; §8 item 5)

Not committed at this stage (it renames a column the shipped storage code
reads, so committing it before the stage-4 code would break `main`'s tests on
this branch). The SQL below is normative; stage 4 commits it verbatim (header
comments may be trimmed, statements not changed).

**Number: `035_llm_org_ai_consent_period`** (was `036`; renumbered at stage 4, see the note at the end of this section). Main's last is `034` (PAC-84,
`32ecacf`); PAC-71 holds `035`. Either order works because this pair touches
only `org_ai_configurations`, `org_ai_confirmations` and reads `settings`,
none of which PAC-71's migration touches. Rule: whichever of PAC-71 and
PAC-87 merges second takes the next free number above every migration on
`main` at its merge time, and never a number below one already on `main`
(golang-migrate never applies a version below the database's current one).
If PAC-87 merges first it keeps `036` only if `035` is already on `main`;
otherwise it renumbers to `035` and PAC-71 renumbers to `036`. Neither text
changes. No stored secret moves (PAC-84 contract 3.3): `api_key` is not read.

*Challenge 2026-11-12 (verified, plus one consequence).* PAC-71's migration is
`035_identity_binding` on its current merge branch (`pac71-merge`; the pushed
`impl/pac-71-identity-binding-backend` still carries `033` and is behind
`main`); it touches only `users` (new columns, `email_key` backfill),
`user_identities` and `operator_audit`, none of which this migration reads or
writes, so the texts are disjoint and the order is free. Why the second to
merge must go above: golang-migrate's `Up` only walks forward from the
database's version, so a `035` merged after a database ran `036` would be
skipped silently on that database. **Consequence for rollback:** the in-place
down step below is `down 1` only while PAC-87's migration is the newest on
the database. If PAC-87 takes `035` and PAC-71's migration lands above it,
PAC-87 can no longer be reverted in place once PAC-71 is deployed (that
would mean running PAC-71's down step first, which the owner excluded in
production on 2026-11-12, option B); it is then fix-forward or the
pre-deploy backup. The preferred order is therefore PAC-71 first, PAC-87 at
`036`. The migration does not change the PAC-84 picture either:
`paceday-operator encryption downgrade-schema` already refuses any database
not at `034` ("roll back the later release first",
`storage/encryption_ops.go:736-746`), so with `036` applied PAC-84's in-place
downgrade needs this down step first, and after PAC-71 ships it ends anyway
(owner, 2026-11-12).

*Stage 4, 2026-11-12 (factory, after review): renumbered to
`035_llm_org_ai_consent_period`.* PAC-71 is blocked on an owner check, so
PAC-87 is expected to merge first; by the rule above it takes `035`
(main's last is `034`) and **PAC-71 renumbers to `036`** when it merges.
The SQL text is unchanged. Consequences, as stated above for this order:
once PAC-71's `036` is deployed, PAC-87 can no longer be reverted in place
(fix forward or the pre-deploy backup). Merge condition for PAC-71 (review
finding 1): its in-request session re-check must also guard the settings
write, i.e. `RecheckSessionGeneration(ctx, tx)` is the first statement of
`SaveSettingsEndingOrgAIConfirmation`'s transaction, before the users-row
lock (`FOR KEY SHARE` then `FOR NO KEY UPDATE` on the same row, no
deadlock). Where the reasoning above names PAC-87's migration `036`, it
now reads `035`, and PAC-71's `035` reads `036`.

### 3.1 Up

```sql
-- (a) consent period start, per organisation; at deploy the current
--     configuration starts its period at its current version (spec §3.8).
ALTER TABLE org_ai_configurations
    ADD COLUMN consent_since_version BIGINT NOT NULL DEFAULT 0;
UPDATE org_ai_configurations SET consent_since_version = configuration_version;
ALTER TABLE org_ai_configurations
    ADD CONSTRAINT org_ai_configurations_consent_since_check
    CHECK (consent_since_version >= 0 AND consent_since_version <= configuration_version);

-- (b) P87-D5: confirmations of members whose stored choice is not
--     'organization' end at deploy (a member with no settings row follows by
--     default, 029_llm_bring_your_own.up.sql:14, and keeps theirs).
DELETE FROM org_ai_confirmations f
WHERE EXISTS (
    SELECT 1 FROM settings s
    WHERE s.user_id = f.user_id AND s.llm_provider <> 'organization'
);

-- (c) a confirmation stores the consent period it covers. A stored value
--     equal to its organisation's current version equals the new
--     consent_since_version (stays confirmed, AC-18 X); an older one is below
--     it and never matches again (P87-D4, AC-18 Y). The CHECK (>= 1) stays.
ALTER TABLE org_ai_confirmations
    RENAME COLUMN configuration_version TO consent_since_version;
-- The column CHECK of 031 keeps its generated name across a column rename;
-- rename it too so the name says what it checks (challenge 2026-11-12).
ALTER TABLE org_ai_confirmations
    RENAME CONSTRAINT org_ai_confirmations_configuration_version_check
    TO org_ai_confirmations_consent_since_version_check;
```

What it does to AC-18's population: X (confirmed the current version 7) ->
stored 7 = consent start 7, confirmed; Y (confirmed 5) -> 5 < 7, row 5; Z
(own provider, holding 7) -> deleted by (b), row 10. After a later key
rotation (version 8, consent start still 7) X still matches. After a later
provider change (consent start = new version > 7) nothing issued before
matches. No settings row is written.

### 3.2 Down

```sql
-- Keep only confirmations of followers recorded in a period that started at
-- the organisation's CURRENT version (no key or destination change since, so
-- recorded at exactly the current state); end all others.
DELETE FROM org_ai_confirmations f
USING org_ai_configurations c
WHERE c.org_id = f.org_id
  AND f.consent_since_version <> c.configuration_version;
DELETE FROM org_ai_confirmations f
WHERE NOT EXISTS (SELECT 1 FROM org_ai_configurations c WHERE c.org_id = f.org_id);
DELETE FROM org_ai_confirmations f
WHERE EXISTS (
    SELECT 1 FROM settings s
    WHERE s.user_id = f.user_id AND s.llm_provider <> 'organization'
);
ALTER TABLE org_ai_confirmations
    RENAME CONSTRAINT org_ai_confirmations_consent_since_version_check
    TO org_ai_confirmations_configuration_version_check;
ALTER TABLE org_ai_confirmations
    RENAME COLUMN consent_since_version TO configuration_version;
ALTER TABLE org_ai_configurations
    DROP CONSTRAINT IF EXISTS org_ai_configurations_consent_since_check;
ALTER TABLE org_ai_configurations DROP COLUMN IF EXISTS consent_since_version;
```

Against AC-19: M (confirmed at S1, key rotated to S2) -> stored S1's period
start, `<>` current version, deleted: row 5 under the reverted code. M2 (row
9) -> not a follower, deleted: row 10/Part 2 row 10. N (confirmed at S2 where
S2 is itself a period start, i.e. a provider change) -> kept, and the
reverted code reads it as confirming S2: correct. If S2 was a key rotation, N
stored the period start S1 and is deleted (asked again): also allowed by
AC-19. Loses only confirmations: no configuration, key, stored choice or
operator record. Confirmations ended by up (b) are not restored.

### 3.3 Rollback procedure

*Corrected by the challenge 2026-11-12.* There is no "operator's migrate
step" in production: `paceday-operator` has only the `encryption`
subcommands (`cmd/paceday-operator/encryption.go:34`), the image carries no
migrate CLI, and `docs/runbook.md` "Rollback" says never to migrate down in
production and to restore the pre-deploy backup (or fix forward). So:

- **Production (binding):** the runbook's procedure, unchanged: fix forward,
  or stop the stack, restore the pre-deploy backup taken before the PAC-87
  deploy, start the previous image. That loses what was written since the
  backup, as for every release with a migration; the runbook already says so.
- **In place (development, tests, a staging copy, or production only if the
  release manager runs golang-migrate against it by hand and records it):**
  1. stop the backend; 2. run the down step (`migrate ... down 1`, valid only
  while PAC-87's migration is the newest applied, see §3 "Consequence for
  rollback"); 3. start the previous image. This is what PAC87-AC-19 tests.
  The down step is the only one in the repository that loses nothing but
  confirmations; a release that wants it as the production rollback adds an
  operator command for it first (not in this contract; open item).

Image-only rollback does not work: the previous image cannot start
against a database at a version it has no file for (`storage/db.go:65`,
`m.Up()`), and must not be forced to, because without the down step the
reverted code would read `org_ai_confirmations` with a column it no longer
has. **Rolling forward again after a rollback re-runs up** (recomputing
`consent_since_version` from the then-current version), so no consent period
from before the rollback is reused. Release note: a rollback asks every
member who uses the organisation's AI to confirm again, except followers
confirmed at exactly the current state; nothing is sent before they do.

## 4. Deploy order and older web builds (spec §8 item 6)

**Backend first, then web. Accepted window, no rewrite of the member's
choice in it.** Because no schema, enum or required field changes (§1, §2), a
web build from before PAC-87 parses every PAC-87 answer. During the window:

- Row 10 shows the server's new sentence and "Review and confirm", which
  confirms only (§2): the stored choice is **not** rewritten. The
  choice-rewriting path from row 10 (`offersOrganizationAI` on
  `personal_keys_forbidden` + `[user]`, `useOrganizationAI.ts:33-36`) is no
  longer reachable because the backend never sends that pair.
- The pre-PAC-87 consent dialog lacks the extra sentence "your own choice is
  kept" (spec §3.6); the notice above it carries it (server `message`).
- Settings for a row-9 member shows the own choice disabled ("kept but not
  used", `Settings.tsx:618-623`) and the AI status as configured; the line
  "AI runs on your organisation's AI (‹provider›)" arrives with the web PR.
- The administrator form lacks the four pre-save sentences of spec §3.6
  (P87-D6) until the web PR: an administrator rotating a key during the window
  is not told that members are not asked again. Accepted (short window; the
  operator record says it, §5).
- The pre-PAC-87 "Use my organisation's AI" in the Settings choice list still
  confirms then writes `organization`; that is the member's explicit choice
  and remains allowed (spec §3.6).
- A pre-PAC-87 web sends whole settings documents when it has no previous
  copy; under §3.4 / P87-D3 an unchanged `llmProvider` no longer ends the
  confirmation, so this does not undo a move.

*Challenge 2026-11-12, checked against smart-calendar-flow `origin/main`
`ae8b0d4` (none of the files below changed since `702a6c2`):*

- Row 10 on an older web: `asksConfirmation` (`aiProviders.ts:112-114`) is
  true and `offersOrganizationAI` (`:120-125`) false, so the notice shows only
  "Review and confirm" (`ai-org-confirm`, `choose: false`); the dialog's
  mutation calls `confirmOrganizationAI` and skips
  `chooseOrganizationAIChoice` when `choose` is false
  (`useOrganizationAI.ts:33-37`). The stored choice is never rewritten from
  row 10. The Settings page additionally shows the existing
  `ai-personal-keys-forbidden` line ("Your own choice (‹provider›) and any
  key you saved for it are kept but not used", `Settings.tsx:618-623`): true
  in rows 9, 10 and 11.
- Nothing an older web shows a row-9 member is false: no notice
  (`aiStatus.configured` is true), the line above (true), the organisation
  section's "Not allowed: members use the organisation's AI"
  (`OrganizationAISection.tsx:74`, true). What is missing is the positive
  line "AI runs on your organisation's AI".
- **Test connection in row 9 (older AND current web).** `llmTestBody`
  (`src/api/client.ts:885-895`) always names the draft's provider, so a
  row-9 member pressing Test connection sends e.g.
  `POST /api/llm/test {"llmProvider":"openai","llmModel":""}` and gets the
  409 `personal_keys_forbidden` `[organization_admin]` "Your organisation
  does not allow personal API keys, so OpenAI cannot be tested. An
  administrator of your organisation can change this."
  (`llm_factory.go:186-197`). True, but the member cannot test the AI they
  actually use from Settings. The web PR may send `{}` in row 9 (the
  manifest's frontend `allowedPaths` now lists `client.ts` for this); not a
  merge condition.
- The older administrator form makes no claim about who is asked again
  (it has no pre-save sentence except the OQ-P2-3 warning), so nothing it
  says becomes false during the window.

**The web PR must not deploy before the backend PR.** A PAC-87 web against a
pre-PAC-87 backend would show row 10 (`personal_keys_forbidden` + `[user]`)
without its choice-rewriting action, and confirming there moves nobody
(Part 2 P2-AC-18). The web PR's body states "requires clockwise-like PAC-87
deployed".

## 5. Audit fields (P87-D7; spec §3.7)

The operator record of every organisation-AI change (`sso_events` kind
`llm_org_ai_written`, `details` JSON, `org_ai.go:455-470`) gains two keys,
in the same transaction as today:

```json
{ "org_id": "...", "via": "stepup|breakglass", "changed": ["apiKey"],
  "configuration_version": 8,
  "consent_period_started": false,
  "consent_since_version": 7,
  "session_iat": 1762934400 }
```

- `consent_period_started` (boolean, always present): true exactly when the
  change altered `provider` (first setup, change, removal), i.e. members will
  be asked again.
- `consent_since_version` (integer, always present): the consent period's
  start after the change (equal to `configuration_version` when
  `consent_period_started`).
- Never a value or a key (P2-AC-6 unchanged). No schema change (`details` is
  JSONB with no CHECK on its keys). No API reads the record
  (`scheduling.yaml`, patchOrganizationAI "No API reads that record"), so no
  client is affected. Confirmations and moves still write no operator record
  (spec §3.7, §5).

## 5a. Settings writes and the confirmation (spec §3.4, AC-13, AC-23)

`patchSettings` ends the caller's confirmation only when the write CHANGES
the stored `llmProvider` to `""` or to a different provider, decided against
the value stored when the write is applied. Binding ordering for stage 4: the
settings write and the end of the confirmation run in one transaction that
locks the caller's settings row (`SELECT ... FOR UPDATE`) before comparing,
and `confirmOrganizationAI` takes the same lock (or an equivalent one on the
caller) before recording, so the two orders of AC-23 are the only outcomes.
Today's handler deletes before saving with no lock
(`handlers_settings.go:213-228`).

**Corrected by the challenge 2026-11-12: the lock is on the caller's
`users` row, not the settings row.** A settings-row lock does not order the
two: the handler creates a missing settings row outside any transaction
(`GetOrCreateSettingsByUser`, `handlers_settings.go:134`), and a member with
no settings row follows by default (`029_llm_bring_your_own.up.sql:14`), so
a confirmation that runs first finds no row and locks nothing. The `users`
row always exists. Binding for stage 4:

1. `patchSettings` runs, in ONE transaction: `SELECT 1 FROM users WHERE id =
   $caller FOR NO KEY UPDATE`; re-read the stored `llm_provider` inside the
   transaction (never the copy read before it, which also loses against a
   second concurrent settings write of the same caller); save; if the stored
   value changed to `""` or another provider, delete the caller's
   confirmations; commit.
2. `confirmOrganizationAI` takes the same `users` row lock FIRST, then the
   configuration row `FOR SHARE` (as today), then records.
3. `FOR NO KEY UPDATE`, not `FOR UPDATE`: an administrator's write holds the
   configuration row `FOR UPDATE` and then inserts its `sso_events` row, whose
   foreign key takes `FOR KEY SHARE` on the administrator's `users` row;
   `FOR KEY SHARE` conflicts with `FOR UPDATE` but not with `FOR NO KEY
   UPDATE`, so an administrator confirming as a member while saving the
   configuration in another request cannot deadlock. A transaction-scoped
   advisory lock keyed on the caller's id is an equivalent alternative.
4. The AC-23 test must run the injected confirmation concurrently (it blocks
   on the lock until the write commits) and assert the outcome, not run it
   inline in the hook, which would wait on itself.

With these, the outcomes are exactly the two orders of AC-23.

## 6. Contract text rewritten (spec §8 item 4)

| Where (scheduling.yaml unless named) | Was | Now |
|---|---|---|
| parseNaturalLanguage, Part 2 paragraph (~436-460) | row 4 sends "exactly the confirmed version"; row 10 waits for choosing `organization`; row 9 reserved | rows 4/9 send one consistent read under a confirmation of the current consent period; row 10 confirms without a settings write; row 9 answered |
| parseNaturalLanguage example `personalKeysForbidden` | row 10 `personal_keys_forbidden` `[user]` | renamed `personalKeysForbiddenUnconfirmed`: row 10 `organization_unconfirmed` `[user]` with the §3.5 sentence |
| testLLMConnection, Part 2 + new "Row 9" paragraph; 400 text; `LLMTestRequest.llmProvider`; `LLMTestResult.configuration` | row 9 reserved | row 9 = row 4 for the probe (AC-14) |
| patchOrganizationAI "What a change does to members" (~1180-1195) | "re-entering the same key re-asks members"; "Forbidding personal keys moves nobody" | split into "to administrators" (unchanged conflict rule) and "to members" (only a provider change re-asks; accepted consequence spelled out) plus "Forbidding personal keys" (row 10 then row 9); audit record gains the consent fields |
| patchOrganizationAI "A version is never reused" | | consent periods never reused either |
| getOrganizationAI | both operations "name" the version | patch names the state changed from; consent names the state shown |
| confirmOrganizationAI description, 200, 409 | "confirms one version"; P2-D2 "confirming does not move" | consent-period rule of §1, row 10 -> row 9 on 200, 409 only on a provider change or incomplete |
| patchSettings bullet (~1635-1645) and `Settings.llmProvider` (~3372-3374) | "Writing `""` or a provider deletes the caller's organisation confirmation, whether or not the stored choice changes" | only a CHANGE of the stored choice ends it; ordering with a concurrent confirmation; row 9 reading of `llmProvider` |
| `Settings.aiStatus` | | row 9 described |
| `AINotConfigured` table and prose (~2620-2660), `message` | row 9 reserved; row 10 `personal_keys_forbidden` `[user]` | rows 9/10 per §2; "Confirmed" per consent period |
| `AIStatus.configured` (~2756), `.configuration` | "rows 2 and 4 (row 9 is reserved)" | "rows 2, 4 and 9"; row 9 reading |
| `OrganizationAIConfiguration.personalKeysAllowed`, `.configurationVersion` (~2860-2880) | "A member's confirmation is for one version" | consent periods; why the period is not exposed |
| `OrganizationAIConsentRequest` | | accepted when the shown version lies in the current period |
| habits.yaml refreshMeetingBrief, Part 2 paragraph | row 4 at the confirmed version | rows 4 and 9 as above; row 10 is the 409 |

## 7. PAC-88 in parallel

PAC-88 (`factory/pac-88-spec`) changes `contracts/openapi/paths/booking.yaml`.
This contract changes only `scheduling.yaml` (organisation-AI, AI probe,
parse and settings text) and `habits.yaml` (the brief-refresh description,
organisation-AI prose). The fragments are disjoint, so the two fragment sets
merge without conflict; the bundle `contracts/openapi/openapi.yaml` and
`backend/api/gen` are regenerated (`make openapi`) by whichever merges second,
and its `contractHash` is recomputed then. No shared component is touched.

## 8. Consumers

| Consumer | Breaks | What happens |
|---|---|---|
| smart-calendar-flow generated types/schemas | no | descriptions only; `REGEN=1` web gate yields a doc-comment diff at most |
| smart-calendar-flow `aiProviders.ts:120-125` `offersOrganizationAI` + `aiProviders.test.ts:148` | behaviour | the `personal_keys_forbidden` + `[user]` branch is dead; web PR removes it and its test |
| `AINotConfiguredNotice.tsx:31,54-63` + test `:75`, `OrgAIConsentDialog.test.tsx:51` | behaviour | row 10 is the `organization_unconfirmed` path; dialog adds the kept-choice sentence when `llmProvider != organization` |
| `useOrganizationAI.ts:33-36` | no | `choose` is no longer used from row 10 (it is not reachable from it) |
| `Settings.tsx:618-623`, `OrganizationAISection.tsx:74,174` | additive | row-9 line; four pre-save sentences (P87-D6) |
| clockwise-like `backend/api/pac73p2_rows_test.go:291` (P2-AC-18), `:351`; `backend/storage/org_ai_test.go:301`; `nlp/llm_factory.go:29-31` | yes | rewritten to PAC-87 AC-1..-23 in stage 4 |
| `e2e/tests/llm-org-ai.spec.ts:119-120` | yes | reads `consent_since_version` (§3) |
| `e2e/TESTIDS-REQUIRED.md:104` (`ai-use-org` rows 7 and 10) | yes | row 7 only; row 10 uses `ai-org-confirm` |
| mcp `clockwise_parse_command` (`mcp/tools.go:195`) | no | passes the sentence through (AC-21); no MCP code reads `reason` (grep finds none outside tests) |
| `mcp/org_ai_not_in_mcp_test.go:22` (fixture: row 10 as `personal_keys_forbidden` `[user]` with the old "Choose ..." sentence) | no (stand-in backend) | stale fixture: AC-21 replaces it with row 10's `organization_unconfirmed` sentence (added by the challenge) |
| `docs/runbook.md` "Rollback" | no | PAC-87's release note and rollback (§3.3) follow it; a line naming the re-ask on rollback may be added (added by the challenge) |

## 9. Verification at this stage

See the manifest's `gates` for the exact commands and results (LF docker
clone, api-gate pattern). Nothing was implemented; statements about
behaviour after PAC-87 are what stage 4 must build.

## Challenge — 2026-11-12

contract-challenger on `52d8a2f` merged with `origin/main` `4f798cb` (CI
workflows only; bundle unaffected). Code read: clockwise-like `origin/main`;
smart-calendar-flow `origin/main` `ae8b0d4` via `git show` (the cited files
are unchanged since `702a6c2`).

**Verdict**: accept-with-changes. The interface change is descriptions plus
one example and is sound; the defects were in the stage-4 bindings (lock,
rollback) and fixed in place above.

### Requests wrongly permitted

None at the schema level: no schema, enum or `required` list changed.
Attacked:

1. `POST /api/organization/ai/consent {"configurationVersion": 7}` after
   P (v7, consent start 7) -> Q (v8, start 8) -> P (v9, start 9): `7 < 9`,
   409 `configuration_changed`. The same if the confirmation's transaction
   runs between the two changes (start 8 > 7). Confirm before both: stored 7,
   then start 9 != 7, so not confirmed. Refused in every interleaving: the
   confirmation reads the row `FOR SHARE` and the admin write holds it
   `FOR UPDATE` (`org_ai.go:425-428`, `:522-525`), so they are serialised.
2. Remove then set up again: `{"configurationVersion": 7}` after `provider:
   ""` (v8, start 8, incomplete) -> 409 `organization_not_configured`
   (wins); after re-setup P (v9, start 9) -> 409 `configuration_changed`.
3. `{"configurationVersion": 12}` with current version 9: `12 > 9`, 409.
   `{"configurationVersion": 0}` is the schema's 400 (`minimum: 1`).

### Responses the client cannot handle

None. Row 10 as `organization_unconfirmed` `[user]` parses under the web's
closed enum (`schemas.ts:8`); no property is added to the
`z.strictObject` schemas (`schemas.ts:8,11,320,329`).

### Consumers affected (added)

- `mcp/org_ai_not_in_mcp_test.go:22`: stale row-10 fixture (stand-in
  backend, so it does not break); AC-21 replaces it. Added to §8.
- `src/api/client.ts:885-895` `llmTestBody`: a row-9 member's Test
  connection always names their own provider and is refused (§4). Added as
  allowed (optional) to the frontend paths.

### Contract/implementation divergences (fixed in the body)

1. **[major] §5a lock target.** "Locks the caller's settings row (`SELECT ...
   FOR UPDATE`)" does not order a confirmation against a settings write: the
   settings row may not exist when the confirmation runs (members with none
   follow by default; the handler creates it outside a transaction,
   `handlers_settings.go:134`), so `FOR UPDATE` locks nothing. Fixed: lock
   the caller's `users` row `FOR NO KEY UPDATE` first in both, re-read the
   stored choice inside the transaction; `FOR NO KEY UPDATE` so an
   administrator's `sso_events` foreign-key `FOR KEY SHARE` cannot deadlock
   against it. Payload: `PATCH /api/settings {"llmProvider":"anthropic"}`
   racing `POST /api/organization/ai/consent {"configurationVersion":9}` for
   a member without a settings row.
2. **[major] §3.3 rollback named a step that does not exist in production.**
   No operator migrate command (`cmd/paceday-operator/encryption.go:34`), and
   `docs/runbook.md` "Rollback" forbids down migrations in production. Fixed:
   production rollback is the runbook's (fix forward, or the pre-deploy
   backup); the in-place down step is for dev/test/staging (AC-19), and an
   operator command for it is an open item.
3. **[minor] Either-order rule, rollback consequence.** If PAC-87 ends up
   below PAC-71's migration, PAC-87 cannot be reverted in place once PAC-71
   is deployed (owner option B forbids PAC-71's down step in production).
   Recorded; preferred order PAC-71 first, PAC-87 at `036`.
4. **[minor] Constraint name.** Renaming the column leaves the 031 CHECK
   named `org_ai_confirmations_configuration_version_check`. Up and down now
   rename it with the column.

### Uncertainties not actually resolved

None claimed (`resolvesUncertain: []`, no `x-uncertain` on the touched
operations; re-checked).

### Verified clean

- Consent period: `consent_since_version <= V <= configuration_version`
  plus complete, checked and recorded under the configuration row lock;
  `ReadOrgAIState` compares stored start to current start in its single
  statement (`org_ai.go:154-177`), so a request never mixes reads. Versions
  are monotonic, and the row is deleted only together with its organisation,
  which also deletes the confirmations (FK cascade), so a version can never
  restart at 1 under a live confirmation (added to §1).
- Migration up: backfill `start = version` keeps X confirmed and Y not;
  P87-D5 delete; rename. Down: `stored = configuration_version` implies
  `stored = start = version` (stored <= start <= version), so a kept row was
  recorded at exactly the current state; non-followers removed. No secret
  column is read (`api_key` untouched; PAC-84's `identityType`/`secrets.go`
  list only `org_ai_configurations.api_key`). PAC-84's `downgrade-schema`
  refuses any database not at `034`, so it is unaffected. PAC-71's `035`
  touches only `users`, `user_identities`, `operator_audit`: disjoint.
- Row 10 on an older web: confirm only, `choose: false`, the stored choice
  is never rewritten (`AINotConfiguredNotice.tsx:30-63`,
  `useOrganizationAI.ts:33-37`). Nothing an older web tells a row-9 or row-10
  member, or an administrator, becomes false during the window (§4).
- Row-9 status (`configured: true`, `configuration: organization`, the
  organisation's `provider`) and probe rules (`llm_factory.go:174-205`:
  step 1a applies before the table, so naming a provider in row 9 is the 409
  and `{}` reaches the row-9 decision).
- Audit fields: `details` is JSONB without a key CHECK; the break-glass
  placeholder row is rewritten with full details in the same transaction
  (`org_ai.go:461-470`), so both new keys are always present on a stored
  change.
- MCP: pure pass-through, no reason parsing.
- Manifest: 23 acceptance tests = spec's 23; the stage-4 paths include the
  migration files, `e2e/tests/llm-org-ai.spec.ts`, `e2e/TESTIDS-REQUIRED.md`;
  added `docs/runbook.md`, the MCP fixture and `client.ts`.
