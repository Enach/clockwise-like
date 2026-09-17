# Spec — PAC-24: A user's settings must be their own, their provider key must not be readable, and saving one preference must not erase the rest

- **Linear**: https://linear.app/paceday/issue/PAC-24/switch-apisettings-and-the-daily-recap-handlers-to-per-user-settings
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**:
  - **Resolved by this spec**: API-002, API-003, API-004, API-009, API-013,
    API-037, API-081, API-082, `x-uncertain` U-18, `x-uncertain` U-19,
    `x-uncertain` U-20 (the recap block-shape marker — resolved by reading, §2.10).
  - **Discovered while writing this spec, not in the register, and in scope
    because they are the same root cause and the fix strands them otherwise**:
    the shared Outlook refresh token and the deployment-wide calendar-provider
    flip (§2.5); the daily-recap fan-out that sends one user's recap to every
    user (§2.6); the per-user settings row that nothing ever creates (§2.4).
  - **Inherited, explicitly not resolved**: API-054 / T8 (raw upstream error
    strings echoed in 5xx bodies). It touches the provider-probe operation this
    spec changes, and this spec adds one narrow requirement against it (AC-14)
    rather than closing it; batch A2 owns it. See §5.

> **A note on this spec's own evidence.** The sandbox this spec was written in
> blocks `proxy.golang.org` and the npm registry, so nothing here was
> established by compiling, running a test, or issuing a request. Every claim in
> §2 comes from reading source and carries a `file:line`. Three claims are
> inferences from reading rather than observations and are marked as such. The
> OpenAPI assembler is pure Python and *was* run.

---

## 0. Scope: why this is one spec and not four

The issue bundles four changes — per-user scoping, a write-only provider key,
merge-instead-of-replace, and a set of validation gaps. Unlike PAC-22 I am *not*
proposing a split, and the reason is that three of the four are load-bearing on
each other:

- **Write-only requires merge.** If the stored key never comes back on a read,
  a client that reads-then-writes cannot send it back. Under today's replace
  semantics that write erases the key. So "stop returning the key" without
  "stop erasing omitted values" is strictly worse than today: it converts a
  disclosure into silent, repeated destruction of the one credential the
  language-model features depend on.
- **Merge requires per-user scoping to be safe to ship.** A merge over a shared
  row still lets any user overwrite any other user's value; it removes the
  *accidental* destruction (§2.3) and leaves the deliberate one. Shipping merge
  alone would close the loudest symptom and leave the defect.
- **Per-user scoping requires a decision about the key.** The moment each user
  has their own row, the question "who inherits the key that is in the shared
  row today" has to be answered, and the only answer that is not a new
  disclosure is "nobody but its owner" (§3, §8 OQ-1) — which is only tolerable
  because write-only makes it un-retrievable anyway.

The fourth — the validation gaps — is genuinely separable and is included only
because every one of them is a one-line check on an operation this work is
already rewriting, and the factory's touch-it-fix-it rule puts them in scope.

**One thing I am splitting out**, and it needs its own issue: the *lifecycle* of
the calendar and conferencing tokens that happen to live in the settings row.
This spec must repoint the code that reads and writes them, because the row they
live in is moving and leaving them behind would strand them (§2.5). It does not
attempt to fix how those tokens are refreshed, revoked, or reconciled with the
provider the user actually connected — that is a separate surface with its own
failure modes, and bundling it would make this change unreviewable. See §8 OQ-6.

---

## 1. Problem

Paceday stores each person's working day: when they start and stop, when they
eat, how long a focus block should be, which language-model provider to use and
the key that reaches it, whether a morning summary goes to Slack and at what
time.

None of it is theirs. Every signed-in account reads and writes one single shared
record. The product presents a personal settings page, and behind it there is one
row that the whole deployment shares.

Four things follow, and each is worse than the last.

**People overwrite each other.** Two colleagues configure their working hours;
the second one to press Save silently replaces the first one's. Nobody is told.
Nothing in the interface suggests the settings are shared, so the first person
has no reason to look, and will find out when the product starts scheduling
their focus time against somebody else's calendar.

**Saving one thing erases everything else.** The save operation is a full
replacement, not an edit. Anything the client does not send back is stored as
blank. Because the interface and the server also disagree about how to spell
every field name (§2.7), what the client sends back is largely unreadable to the
server — so a save can wipe the timezone, the focus configuration, the morning
summary schedule and the provider key in one request, from a user who only meant
to move lunch.

**The provider key is readable by everybody.** The key that bills the
organisation for every language-model call is returned in clear text to any
signed-in account that asks for its own settings. It does not have to be
attacked for; it is in the ordinary response the settings page already fetches.

**And one person's calendar connection becomes everyone's.** The credential that
reaches an Outlook calendar is stored in that same shared record. One user
connecting Outlook replaces whatever was there, switches the whole deployment to
Outlook, and makes their calendar the one the product reads availability from —
for every user (§2.5).

**Who is affected.** Every user of any deployment with more than one account.
The single-user deployment is unaffected in practice, which is precisely why
this has survived: the product is correct for exactly one user and silently
wrong for the second.

**What it costs them.** Lost configuration with no record of what it was; a
billing credential exposed to everyone who can sign in and therefore to be
treated as compromised; and, for anyone whose calendar connection was replaced,
a period during which the product was reading and writing against a colleague's
calendar.

---

## 2. Current behaviour

All citations are from reading; nothing was executed (see the note above).

### 2.1 The settings record is a singleton, pinned by primary key

`GetSettings` selects `FROM settings WHERE id = 1`
(`backend/storage/settings.go:227`). `SaveSettings` inserts `VALUES (1, …)` with
`ON CONFLICT (id) DO UPDATE` (`settings.go:290-294`). When no such row exists,
`insertDefaultSettings` creates it with `INSERT INTO settings (id) VALUES (1)
ON CONFLICT (id) DO NOTHING` (`settings.go:262`). The per-day schedule columns
are read and written by two further statements keyed the same way
(`settings.go:479`, and `settings.go:501-507`, where `saveScheduleFields`
defaults `id` to 1 whenever `s.ID == 0` — which is always, on the write path,
because `Settings.ID` carries `json:"-"` (`settings.go:146`) and is therefore
zero in anything decoded from a request body).

`GetSettings` is called from **22 non-test call sites**: the settings read and
write (`backend/api/handlers_settings.go:32,58`), all four daily-recap handlers
(`handlers_daily_recap.go:29,47,94,113`), the auth status handler
(`handlers_auth.go:85`), three conferencing handlers
(`handlers_conferencing.go:130,161,321`), the provider probe
(`handlers_llm.go:17`), meeting briefs (`handlers_meeting_briefs.go:73`), the
Microsoft callback (`handlers_microsoft_auth.go:61`), and seven engine or parser
entry points (`engine/analytics.go:58`, `engine/compression.go:46`,
`engine/daily_recap.go:72`, `engine/freebusy_service.go:75`,
`engine/habits.go:98`, `engine/personal_blocker.go:97`,
`engine/smart_schedule.go:53,286`, `nlp/parser.go:173`).

Every one of those routes sits behind `requireAuth`
(`backend/api/routes.go:57,64`), which puts the caller's id in the request
context (`backend/api/middleware.go:52-54`) — so the identity is present at
every call site and is simply not used.

### 2.2 The row is not global by design; it belongs to one real user

This matters for the migration decision, so it is worth stating precisely.

`settings.user_id` was added in `migrations/006_auth.up.sql:15` as a nullable
column with `REFERENCES users(id) ON DELETE CASCADE`. Migration 018 did **two**
things and no more (`018_per_user_settings_and_calendars.up.sql`):

1. a best-effort backfill — `UPDATE settings SET user_id = (SELECT id FROM users
   ORDER BY created_at LIMIT 1) WHERE user_id IS NULL AND EXISTS (SELECT 1 FROM
   users)` (`:14-17`);
2. `ADD CONSTRAINT settings_user_id_unique UNIQUE (user_id)` (`:21-22`).

Its `.down.sql` is one line: drop that constraint.

It did **not** add the column (006 did), did **not** set `NOT NULL`, and did not
change a single query. Its own comment says so and says why: the singleton path
"doesn't yet supply a user_id", the cron queries filter `user_id IS NOT NULL`,
and a "Follow-up ADR will migrate handlers to always supply user_id and then
tighten this constraint" (`:5-10`).

So after 018, on any deployment with at least one user, the `id = 1` row carries
the `user_id` of the **earliest-created user**. The shared record is not an
ownerless global — it is one identifiable person's settings row, which every
other account has been reading and writing.

Two hazards in 018 that the follow-up must not repeat:

- **It is unsafe on more than one unattributed row.** The backfill sets every
  `user_id IS NULL` row to the *same* user id, and the unique constraint is added
  two statements later — so a database with two such rows fails the migration
  halfway. In practice at most one can exist, because the only creator is the
  `id = 1` insert, but the shape is wrong and the replacement must attribute or
  delete rather than blanket-update.
- **`id` is a `SERIAL`** (`001_initial.up.sql:2`) and the `VALUES (1)` insert
  bypasses the sequence, so the sequence and the table can disagree about which
  ids are free.

### 2.3 Saving is a full replace, and the client cannot address most of it

The write handler decodes the body into a fresh, zero-valued struct
(`handlers_settings.go:42-43`) and passes it straight to `SaveSettings`, whose
`INSERT` lists **47 columns** and whose `ON CONFLICT DO UPDATE` assigns every one
of them from `EXCLUDED` (`settings.go:270-349`). There is no read-before-write.
Any property absent from the request is therefore persisted as the Go zero
value — empty string, zero, false.

Only three stored values survive an omission, and only because `SaveSettings`
does not list their columns at all: `microsoft_tokens`, `zoom_tokens` (both
`json:"-"`, `settings.go:191,196`) and `working_hours`/`lunch_breaks`, which are
written separately (`settings.go:499-509`).

The response is a fresh read (`handlers_settings.go:58`), so the caller is shown
the post-wipe state and not their own submission — which means a client that
diffs what it sent against what came back *could* detect this, and none does.

### 2.4 Nothing ever creates a per-user settings row

`GetSettingsByUser` exists (`settings.go:420-473`) and returns `(nil, nil)` when
there is no row (`:463-465`). Four production paths use it:
`engine/focus_time.go:76`, `engine/auto_decline.go:66`, `engine/habits.go:70`
and `engine/habits.go:96`.

A grep for `INSERT INTO settings` across the tree returns exactly four hits: the
`VALUES (1)` default insert (`settings.go:262`), the multi-column singleton
upsert (`settings.go:271`), and two **test** fixtures
(`backend/engine/focus_time_test.go:318`,
`backend/scheduler/cron_test.go:43`). The e2e seed inserts four
(`e2e/seed/seed.sql:79-90`, and it already uses `ON CONFLICT (user_id)`, so the
per-user shape is what the test data assumes).

**No production code path ever creates a settings row for a user.** The
consequence is a live defect independent of everything else in this spec: for
every user except the one migration 018 happened to attribute,
`FocusTimeEngine.RunForUser` returns `no settings row for user <id>`
(`focus_time.go:82-84`) and the auto-decline service silently does nothing
(`auto_decline.go:69-71`). Focus-time scheduling is broken for every user but
the first, today, and no test covers it.

### 2.5 The Outlook credential and the deployment's calendar provider are shared

`SaveMicrosoftToken` is `UPDATE settings SET microsoft_tokens = $1,
calendar_provider = 'outlook' WHERE id = 1`
(`backend/auth/microsoft_oauth.go:57`). `LoadMicrosoftToken` is the matching
`SELECT … WHERE id = 1` (`:62`) and takes no user id at all.

It is written from one place — the Microsoft OAuth callback
(`handlers_microsoft_auth.go:66`) — and read from four:
`handlers_auth.go:98`, `handlers_conferencing.go:40,138,168`, and
`engine/freebusy_service.go:123`.

So one user completing the Outlook connection (a) overwrites whatever refresh
token was stored, (b) switches the *whole deployment's* calendar provider to
Outlook, and (c) makes their token the one the availability engine uses for
everybody. This is broader than API-009 records — that row says one user's
Outlook *address* is reported to all; the credential and the provider switch are
not in the register.

The Zoom tokens are the same shape: `SaveZoomTokens`
(`backend/storage/personal_calendars.go:154-160`) and `ClearZoomTokens`
(`backend/storage/conferencing.go:5-8`) both write `WHERE id = 1`, the second
also resetting `conferencing_provider` for everyone.

### 2.6 The morning recap is sent to everybody if any one person enables it

`DailyRecapService.RunAll` — the every-minute cron — is:

```
SELECT u.id, u.name FROM users u
INNER JOIN settings st ON st.id = 1
WHERE st.recap_enabled = true
```

(`engine/daily_recap.go:124-127`). The join has no correlation between `u` and
`st`. If the shared row has the recap enabled, this returns **every user**, and
`RunForUser` then re-reads the same shared row for the send time, the timezone,
the destination and the content flags (`daily_recap.go:72`). Anyone with a Slack
connection receives a summary of their day at a time and to a destination
somebody else chose, and cannot turn it off, because turning it off turns it off
for everyone.

The four HTTP recap handlers read the same row (`handlers_daily_recap.go:29,47,
94,113`), and the PATCH is a genuine field-level merge in the handler
(`:59-83`) that then calls the full-row `SaveSettings` (`:85`) — so it rewrites
all 47 columns from the values it just read, including re-running the per-day
schedule normalisation. It cannot lose data on its own, but it makes every
recap edit a full rewrite of an unrelated user's entire configuration.

### 2.7 The interface and the server agree on the spelling of exactly one field

The server's wire names come from struct tags on `storage.Settings`
(`settings.go:145-206`) and are camelCase: **50 properties** are exposed
(three more carry `json:"-"`).

The interface's type declares **34 properties** in snake_case
(`smart-calendar-flow/src/api/types.ts:49-88`).

The adapter between them, `normalizeSettings`
(`smart-calendar-flow/src/api/client.ts:526-547`), spreads the raw response
verbatim and then bridges **four** properties by hand — the two schedule objects
and the two meeting-policy values (`:528-543`). `settingsRequestBody`
(`:550-563`) bridges the same four in the other direction.

I counted the rest rather than taking the audit's "about 4 of ~48":

- **1** property is spelled identically in both conventions (`timezone`) and so
  needs no bridge.
- **26** of the interface's properties have a server counterpart, are not
  bridged, and differ by case — so they read as `undefined`, every time. Among
  them are the working hours, the lunch window, the focus configuration, the
  provider selection and the provider key.
- **3** interface properties have no server counterpart at all.
- **19** server properties have no interface counterpart, including all seven
  recap properties, the three buffer refinements, and the calendar address.

The settings page reads the snake_case names directly — `draft.work_start`
(`src/pages/Settings.tsx:293`), `draft.llm_api_key`
(`:712`) — so against a real server those inputs render empty. The adapter's
test suite passes because its fixture is hand-built in snake_case
(`src/api/settingsAdapter.test.ts:13-20`), a shape the server never sends.

There is a second-order effect that is easy to miss and is the mechanism behind
§2.3's data loss. Because `normalizeSettings` spreads the raw response, the
camelCase properties survive into the object the page edits, and
`settingsRequestBody` spreads them back out — so the *untouched* values do round
trip. What does not round trip is anything the user edited, because the edit was
written under a snake_case name the server's decoder does not recognise
(`encoding/json` matches tag names case-insensitively but does not ignore
underscores). **The user's edits are the part that is discarded, and the
untouched values are the part that persists.** *This is an inference from
reading both sides, not an observed request.*

### 2.8 The provider probe ignores what it is sent

`testLLM` never touches `r.Body`; it loads the stored settings and probes those
(`handlers_llm.go:16-21`). The interface posts the entire edited settings object
to it, provider key included (`src/api/client.ts:1141-1143`). So the key is put
on the wire in a request that discards it, and the button labelled "Test
connection" reports on the saved configuration rather than the typed one.

### 2.9 Validation gaps

`validateSettings` (`handlers_settings.go:67-120`) checks four time fields for
`HH:MM` shape, six integers for non-negativity, the cron expression, the
provider name, and the per-day schedules. It does not check:

- that the end of the working day is after its start, or that lunch ends after
  it starts, for the legacy global pair (`:68-78` checks format only);
- that the minimum focus block does not exceed the maximum (`:80-85`);
- `bufferMinMeetingMinutes` at all — its two siblings are checked (`:95-98`) and
  it is not;
- the timezone, which is accepted as any string and falls back to UTC at use
  (`engine/auto_decline.go:85` via `settingsLocation`);
- the recap destination, which *is* checked on the recap PATCH
  (`handlers_daily_recap.go:66-69`) and not on the settings write — so a bad
  value reaches the database `CHECK` (`015_daily_recap.up.sql:5-6`) and surfaces
  as a 500 (API-081);
- the recap send time, checked on neither path, and cast by Postgres to `TIME`
  during the save — so an unparseable value is also a 500;
- the calendar provider, accepted as any string and falling through to the
  Google branch at every consumer (`calendar/client_factory.go:15-31`,
  `handlers_auth.go:91-119`, `engine/freebusy_service.go:119-130`). This is U-18.

### 2.10 The `x-uncertain` markers on these operations, and what I could settle

Three markers sit on the operations this work changes
(`contracts/openapi/paths/scheduling.yaml`):

- **U-18** (`:1384`) — the accepted set of calendar-provider values. Settled by
  reading: three consumers switch on the value and all three accept exactly
  `outlook` and `webcal` and treat everything else as Google
  (`calendar/client_factory.go:16,22,27`, `handlers_auth.go:97,105,111`,
  `engine/freebusy_service.go:122`). The authoritative set is therefore
  `google | outlook | webcal`, and nothing enforces it.
- **U-19** (`:1406`) — the rendered form of the recap send time. The column is
  `TIME NOT NULL DEFAULT '08:00'` (`015_daily_recap.up.sql:3`) and is read as
  `recap_send_time::TEXT` (`settings.go:224`). Postgres renders `time` as
  `HH:MM:SS`, with a fractional part only when one was stored, so the response is
  `08:00:00` and not the `"HH:MM"` the struct comment claims
  (`settings.go:199`). *This is derived from Postgres's documented output format,
  not from an observed response* — I could not reach a database. It is
  falsifiable by one integration test (§4, AC-16).
- **U-20** (`:1595`) — the Slack block shape returned by the recap preview.
  Settled by reading `BuildMessage` end to end
  (`engine/daily_recap.go:150-236`, plus `buildSummaryBlock` `:238-262` and
  `buildMeetingBlocks`): the top-level `type` values are exactly `header`,
  `context`, `section` and `divider`, and the only nested text types are
  `plain_text` and `mrkdwn`.

### 2.11 What tests exist over this area today

- `backend/storage/settings_test.go` — four tests, all against the singleton:
  defaults (`:7`), idempotence (`:33`), save (`:48`), update (`:106`). Every one
  of them asserts the current shared-row behaviour and **must change**.
- `backend/api/handlers_settings_test.go` — nine tests (`:13`–`:163`). Four
  drive the handlers directly with no user in the request context
  (`:13,34,63,76`); the rest test `validateSettings` as a pure function. The
  four handler tests must change; the five validator tests survive.
- `backend/api/handlers_settings_schedule_test.go` — two pure validator tests
  (`:9`, `:27`). Both survive.
- `backend/api/testhelpers_test.go:36` — `openTestDB` returns a **single shared
  database** for the whole package, created once in `TestMain` (`:14-33`). There
  is no per-test isolation, so per-user tests must create their own users; the
  helpers to do that exist (`createTestUser`, `withUser`, both in
  `handlers_teams_test.go:43-61`).
- **No test anywhere covers**: the recap handlers, the auth status handler's
  settings branch, the provider probe, the recap cron's fan-out, or any
  cross-user isolation. `e2e/tests/` has six specs and none touches settings.
- The interface's `src/api/settingsAdapter.test.ts` — eleven assertions across
  four tests, every one of them built on a fixture the server does not produce
  (§2.7).

All eight affected operations are `handwritten` in
`contracts/openapi/MIGRATION.md:68,96-99,119,153-154`.

---

## 3. Desired behaviour

**A person's settings are their own.** What someone sees on their settings page
is what they saved, it is unaffected by anything a colleague does, and what they
save affects nobody else. This holds for every part of the settings — the
working day, the focus configuration, the morning summary, the provider
selection — and for every way the product reads them, including the background
jobs that run while nobody is signed in. A user who has never opened the
settings page still has settings: they get the product's defaults, and those
defaults are theirs to change.

**Saving one preference changes only that preference.** A request that says
nothing about a value leaves it exactly as it was. There is no way to erase
something by not mentioning it. Erasing is possible, but only by asking for it:
a request that supplies an empty value is a request to make it empty, and is
honoured as such. There is no third state — the absence of a value and an empty
value are different requests with different outcomes, and both are predictable
from the request alone without knowing what was stored.

**The key that reaches the language-model provider can be given, replaced and
removed, and can never be read back.** Nobody retrieves it — not the person who
entered it, not a colleague, not an administrator. What a reader can learn is
whether a key is on file, because "not configured" and "configured" are states a
person setting this up needs to tell apart, and knowing that a key exists is not
knowing the key. The consequence is deliberate and must not be softened later:
someone who loses their key re-enters it rather than looking it up.

**Trying out a provider configuration tests what the person is looking at.** The
control that checks whether the provider is reachable checks the configuration
being offered, including a key that has just been typed and not yet saved. When
no configuration is offered it falls back to what is stored, so the check
remains available to something that has none to offer. Whatever it reports, it
does not repeat the key back.

**A configuration that cannot work is refused when it is offered.** A working
day that ends before it starts, a minimum longer than its maximum, a timezone
that does not exist, a destination or a time the product cannot use — each is
refused at the point of entry with a message naming what is wrong, instead of
being stored and failing later as an internal error or silently falling back to
something the person did not choose.

**A calendar connection belongs to the person who made it.** Connecting a
calendar changes that person's calendar, that person's availability and that
person's view of whether they are connected. It does not move anyone else's
calendar, and it does not change which kind of calendar the product uses for
anybody else.

**The morning summary goes to the people who asked for it.** Enabling it enables
it for the person who enabled it. Someone who has not enabled it does not
receive one, and someone who has receives it at the time and to the destination
they chose.

**Settings that exist today are not silently redistributed.** The values
currently in the shared record belong to the person the record belongs to, and
they keep them. Everyone else starts from the product's defaults for anything
that identifies a person or authorises spending — credentials, calendar
addresses, message destinations — because copying those would turn today's
disclosure into a permanent one. Everything else — the shape of the working day
and how focus time is scheduled — is copied to everyone as a starting point,
because it is what those users have been seeing and editing, and losing it would
be a visible regression for no security benefit. §8 OQ-1 records the exact
partition and asks for it to be confirmed before the migration is written.

---

## 4. Acceptance criteria

Each is falsifiable today: none of these tests exist (§2.11), and each fails
against current behaviour.

**Per-user isolation**

AC-1. Given two signed-in users, when the first saves a distinctive working-day
setting and the second then reads their own settings, then the second sees the
product default and not the first user's value; and when the second saves a
different value, the first user's value is unchanged on a subsequent read.
`[contract]`

AC-2. Given a user who has never saved any settings, when they read their
settings, then they receive a complete, valid settings document containing the
product defaults, and a second read returns the same document rather than
creating a further one.  `[contract]`

AC-3. Given a user who has never saved any settings, when a background job that
needs that user's settings runs for them, then it uses that user's settings and
completes, rather than failing because no settings exist for them.  `[unit]`

AC-4. Given two signed-in users, when the first enables the morning summary and
chooses a destination, then the second's morning-summary settings are unchanged;
and when the scheduled send runs, only the first user is sent one.  `[unit]`

AC-5. Given two signed-in users where the first has connected a calendar of a
kind the second has not, when the second asks whether their calendar is
connected, then the answer describes the second user's own connection and
contains no address, provider or connection state belonging to the first.
`[contract]`

**Editing without erasing**

AC-6. Given stored settings in which several values are non-default, when a
request supplies exactly one of them with a new value, then that value changes
and every other value is exactly what it was before the request.  `[contract]`

AC-7. Given a stored setting with a non-empty value, when a request supplies
that setting with an empty value of its type, then the stored value becomes
empty — so clearing remains possible and is distinguishable from omitting.
`[contract]`

AC-8. Given stored settings, when a request supplies an explicit null for any
setting, then the request is refused with a client error naming the setting, and
nothing is stored.  `[contract]`

AC-9. Given stored settings that include per-day working hours for several days,
when a request supplies the per-day working hours with one day removed, then the
stored per-day hours are exactly what was supplied — a day omitted from a
supplied group is removed, not retained.  `[contract]`

AC-10. Given stored settings, when a request supplies a value the server
manages itself, then the request succeeds and the server-managed value is
unaffected by what was supplied.  `[contract]`

**The provider key**

AC-11. Given stored settings with a non-empty provider key, when any signed-in
user reads any settings representation from any operation, then no value
anywhere in any response equals the stored key, under any name.  `[contract]`

AC-12. Given stored settings with a non-empty provider key and another user's
with none, when each reads their own settings, then the responses differ in a
property whose only meaning is whether a key is on file, and that property is
`true` exactly when the stored key is non-empty.  `[contract]`

AC-13. Given stored settings with a provider key, when the settings are read and
the returned representation is submitted back unchanged, then the stored key is
unchanged; and when the same representation is submitted with an explicitly
empty key, then the stored key becomes empty.  `[contract]`

AC-14. Given a request to check whether the provider is reachable that supplies
a provider configuration including a key, when the check fails for any reason,
then the failure is reported and no response property contains the supplied key
or the stored one.  `[contract]`

AC-15. Given stored settings naming one provider and a request to check
reachability that supplies a *different* provider, then the check reports on the
supplied provider; and given the same request with no configuration supplied,
then it reports on the stored one.  `[contract]`

**Refusing what cannot work**

AC-16. Given a request that supplies a time-of-day value for the morning
summary, when the value is well formed, then reading it back yields a value of a
single documented shape that a client can parse without guessing; and when it is
malformed, the request is refused with a client error rather than failing
internally.  `[contract]`

AC-17. Given a request that supplies a working day whose end is not after its
start, a minimum focus block larger than its maximum, a negative value for any
duration, a timezone the product cannot resolve, a summary destination outside
the permitted set, or a calendar kind outside the permitted set, then in each
case the request is refused with a client error naming the offending setting,
and nothing is stored.  `[contract]`

**What must keep working**

AC-18. Given a deployment with exactly one user whose settings are configured,
when that deployment is upgraded, then that user's settings — every value,
including the provider key and any calendar credential — are unchanged and in
use afterwards.  `[e2e]`

AC-19. Given a deployment with several users at the moment of upgrade, when a
user other than the one the record belonged to reads their settings afterwards,
then they see the working-day and focus values that were in the shared record,
and they see no credential, calendar address or message destination belonging to
anyone else.  `[unit]`

AC-20. Given the settings page, when it is opened against a server, then every
setting it displays shows the stored value rather than a blank, and when one is
edited and saved, a subsequent read returns the edited value.  `[e2e]`

**Note on how AC-3, AC-4, AC-18, AC-19 and AC-20 are proven.** AC-3 and AC-4
describe background jobs with no HTTP surface and belong in unit tests against a
real database. AC-18 and AC-19 describe a migration and can only be proven by
applying it to a database seeded to look like the "before" state — stage 3 must
name where that fixture lives. AC-20 is the only criterion that requires the
interface, and it is the one that proves §2.7 is actually fixed; it cannot be
written until the contract has merged and the types have regenerated.

---

## 5. Explicitly out of scope

- **Encrypting the provider key, or any credential, at rest.** They are plain
  text in the database today (`001_initial.up.sql`) and remain so. This spec
  stops the key crossing the boundary, which is the reported finding. Encryption
  is a separate piece of work with its own key-management questions.
- **Rotating the provider key that has already been exposed.** Every key
  configured before this ships has been readable by every account and must be
  treated as compromised and rotated by whoever owns the billing relationship.
  Closing the leak does not un-expose it. This needs a named owner — §8 OQ-5.
- **The lifecycle of the calendar and conferencing credentials.** This spec moves
  them with the record they live in and repoints the code that reads them,
  because leaving them behind would strand them (§2.5). It does not address how
  they are refreshed, revoked, or reconciled with what the user actually
  connected. §8 OQ-6.
- **Whether settings should be scoped to something other than a person** — an
  organisation, a team. Some values plausibly belong to a deployment rather than
  a person (§8 OQ-2). This spec scopes everything to the person, because that is
  what the interface presents and what the schema was already prepared for, and
  records the question.
- **Sanitising the error strings the product echoes on failure** (API-054 / T8).
  The provider-probe operation returns upstream error text verbatim today
  (`handlers_llm.go:27,31`). AC-14 requires only that the key never appears in
  such a response; making the whole class of responses safe is batch A2.
- **A settings history, or any record of what a value used to be.** Nothing
  records who changed a setting or what it was, so the configuration overwritten
  under the shared record cannot be recovered or attributed (§7). Adding that is
  a feature.
- **Any adjacent operation that reads the shared record but is not named here.**
  Several exist (§2.1 lists 22 call sites; this spec's criteria reach eight
  operations plus the background jobs). The rest read the record for values this
  spec does not change; they must be repointed mechanically, and the plan must
  enumerate them, but no criterion covers them individually.

---

## 6. What must not change

- **A single-user deployment must come out of this byte-for-byte equivalent.**
  That is the deployment that exists, so it is the one that must be provably
  unharmed. No test protects it today. AC-18 is the new guard.
- **The shape and meaning of every setting that is not the provider key.** This
  work changes who owns a value and how a value is supplied; it does not change
  what any value means. The five validator tests in
  `backend/api/handlers_settings_test.go:92-163` and both tests in
  `handlers_settings_schedule_test.go` protect the existing validation rules and
  must keep passing unchanged.
- **The per-day schedule normalisation.** Reads currently rewrite an unset
  working-hours mode into a full default and a nil day map into an empty one
  (`settings.go:92-106`), and consumers depend on that
  (`settings.go:110-143`). It must survive the move to per-user records. No
  handler test covers it; the two schedule validator tests cover only the
  validator.
- **The four storage tests in `backend/storage/settings_test.go` will all have to
  change**, and that is a behaviour change this spec declares rather than a
  sign that the tests were wrong: they assert the singleton contract
  (`:43-45` asserts that a second read does not create a second row, which under
  per-user records becomes a per-user assertion). Each must be rewritten to the
  per-user equivalent, not deleted.
- **Background jobs must not start failing for users they currently serve.** The
  recap cron currently serves everyone (wrongly, §2.6); after this it serves
  fewer people. That is the intended change and AC-4 asserts it. What must not
  happen is the opposite: a job that works for the first user today must not
  start erroring for them.
- **Nothing may be deleted by the migration except a record belonging to no
  user.** §7 depends on this.

---

## 7. Rollback

**There is a migration, and its down migration does not restore the prior
state — but it does not destroy data either, and the distinction matters.**

The forward migration attributes or removes any record belonging to no user,
creates one record per user, and tightens the ownership column so a record
without an owner can no longer exist. The down migration loosens the column
again. It must **not** delete the records it created: those records hold every
setting every user has saved since the upgrade, and deleting them is the one
irreversible act available here.

So after a revert:

- **Every user's settings still exist in the database and are no longer read by
  anything.** The reverted code reads one record by primary key again; the
  others are inert. Nothing is lost, and re-applying the migration picks them
  back up — provided the forward migration never renumbers or moves the original
  record, which is why it must not.
- **The reverted code resumes overwriting that one record on every save**, so a
  revert re-opens the data-loss defect immediately and will begin destroying the
  configuration of whoever owns that record. A revert is therefore not a neutral
  act; it should be treated as reopening two critical findings, and fixing
  forward is preferable.
- **Provider keys entered after the upgrade become unusable and unreadable.**
  They were stored in the user's own record, which the reverted code does not
  read, and the write-only rule means nobody can retrieve them to re-enter. They
  are in the database, so nothing is lost, but recovering them requires database
  access. This gets worse the longer the release has been live.
- **Anything a colleague overwrote before the upgrade is gone and stays gone.**
  The shared record holds one set of values with no history (§5), so the
  configuration that users lost to each other cannot be recovered by this work or
  by reverting it.

The behaviour changes a client could notice — the absent provider key, the new
indicator, the refusal of previously-accepted configurations, the change in how
a save is expressed — all revert with the code, because no state depends on them.
The one exception is the interface: if the interface has moved to the new write
operation and the server reverts, saving breaks entirely until the interface
reverts too. §8 OQ-3 asks whether the two can be reverted independently.

---

## 8. Open questions

**OQ-1 — Which settings are copied to every user by the migration, and which are
not?** *Owner: human (product), before the migration is written. Blocks stage 3.*
§3 states the principle — preferences are copied, anything identifying or
authorising is not — and this is my proposed partition, which I am asking to have
confirmed rather than assumed:

| Copied to every user | Left at the product default for everyone but the owner |
|---|---|
| Working day start and end, per-day working hours, lunch window and per-day lunch overrides, whether lunch is protected | The language-model provider key |
| Focus block minimum, maximum and daily target; focus label and colour | The Outlook credential and the Zoom credential |
| Buffer durations and buffer rules | The calendar address |
| Timezone | The calendar kind and the conferencing kind |
| Out-of-hours meeting allowance; whether out-of-hours invitations are auto-declined | The external calendar URL |
| Compression and auto-schedule enablement and schedule | The morning-summary destination, channel and enablement |
| The provider *selection* and model name, and the non-secret provider endpoints | The morning-summary send time |

Two of these are genuinely arguable and are the reason this is a question rather
than a decision. **The provider selection and model without the key** leaves
every user configured to use a provider they cannot reach, which produces a
clear error but is arguably worse than leaving them unconfigured. **The
timezone** is a preference by nature but in a single-region deployment it is
effectively a deployment default, and copying it is almost certainly right;
in a distributed one it is a guess. If the answer is "we cannot tell", the safe
reading is: copy the timezone, do not copy the provider selection.

**OQ-2 — Is every setting in this record actually a property of a person?**
*Owner: human (product). Does not block, but changes what OQ-1 means.* Some
values read like deployment configuration rather than personal preference — the
provider endpoints, the region and profile, the local model host. Scoping them
per-user means every user configures them separately, which for a self-hosted
deployment is busywork. This spec scopes everything per-user because that is what
the schema and the interface already assume, and because a split is a larger
change. If the answer is "some of these are deployment-level", that is a separate
issue and this spec is still correct in the interim.

**OQ-3 — Are the two repositories deployed together, and can they be reverted
independently?** *Owner: whoever operates the deployment. Blocks the contract's
decision about retiring the old write operation.* The factory's ordering rule
puts the server change first and the interface change second, so there is a
window in which a deployed interface talks to a changed server. Whether that
window is minutes or weeks determines whether the previous write operation must
be kept as a compatibility shim at all, and §7's last paragraph turns on the same
answer. Resolve by asking; if the answer is "they deploy together", the shim can
be dropped and the contract is simpler.

**OQ-4 — What should a user see the first time they open their settings after
the upgrade: the values they had been editing, or their own defaults?** *Owner:
human (product). This is OQ-1 restated from the user's side and should be
answered with it.* Under my proposal they see most of what they were editing,
with the credentials and destinations blank. The alternative — everyone starts
clean — is more honest about what happened but presents as "the product forgot my
settings" to every user at once, with no message explaining it. Whichever is
chosen, somebody has to decide whether users are *told*, and this spec cannot
decide that on its own.

**OQ-5 — Who tells the deployment's owner to rotate the language-model provider
key?** *Owner: human.* Out of scope for the code (§5) and cannot be dropped: the
key has been readable by every account and closing the read path does not
un-expose it. Needs a named owner and a sequence, because rotation means the
product cannot reach the provider until the new key is entered — and after this
work, entering it is the only way to set it, since it can no longer be read back
to confirm.

**OQ-6 — Does moving the calendar credential into a per-user record actually
make the calendar connection work per user?** *Owner: whoever writes the
follow-up issue; must be filed before this ships.* This spec repoints the code
that reads and writes that credential (§2.5) because the record is moving. What I
could not establish by reading is whether the rest of that path — token refresh,
the provider-availability reporting, the conferencing status — is correct once
each user has their own. It may be that moving the credential exposes further
defects immediately. The cheapest way to settle it is to write AC-5 as an
integration test first and see what else fails.

**OQ-7 — Should the previously-shared record's owner be told that it was
shared?** *Owner: human.* Their settings were being overwritten by colleagues,
and their provider key and calendar credential were readable by everyone. They
are the person with the most to act on and the only one this spec can identify.
Raised here because it is the kind of thing that gets lost between a spec and a
release note.

**OQ-8 — Can the contract gate pass if an operation this work touches carries an
`x-uncertain` I resolved by reading rather than by observing?** *Owner:
contract-challenger, to push back on.* I settled U-18 and U-20 by reading source,
which I am confident in, and U-19 by reasoning from Postgres's documented output
format, which I am not — I could not reach a database (§2.10). AC-16 is written
to force that one to be settled by a test rather than by my reasoning. If the
challenger thinks a marker resolved by reading is not resolved, say so now
rather than at the gate.

---

## Challenge — 2026-09-17

*spec-challenger. I did not write this spec and did not ask the author for their
reasoning. Everything below was re-derived from source. Nothing was compiled or
executed except `scripts/openapi_assemble.py`, which is pure Python.*

**Verdict**: accept-with-changes

The evidence base is unusually good — I re-counted §2.7 from scratch and every
one of the six numbers is exact, and §2.1–§2.6, §2.8 and §2.9 hold at every
`file:line` I checked. The changes below are three things §2 gets wrong or
understates and one partition decision that, as written, causes a mass calendar
write on release day.

### Blocking

1. **§2.4 is right but scoped too narrowly, and one case is worse than stated.**
   The defect is not confined to background jobs. `POST /api/focus/run` sits
   behind `requireAuth` and reaches the same path: `backend/api/handlers_focus.go:37`
   calls `eng.Run`, which at `backend/engine/focus_time.go:59-64` pulls the
   caller's id from the context and delegates to `RunForUser`, which returns
   `no settings row for user <id>` at `:82-84`. So this is a live HTTP 500 for
   any signed-in user without a row, not only a silent cron failure.
   Separately: 018's backfill is guarded by `AND EXISTS (SELECT 1 FROM users)`
   (`018_per_user_settings_and_calendars.up.sql:17`). On a deployment whose
   migrations ran before anyone signed up — the normal case for a fresh
   install — the guard is false, nothing is ever attributed, and focus-time is
   broken for **every** user including the first. The spec's "every user but the
   first" is the upgrade case only.
   *Resolves it*: §2.4 states the HTTP surface and the zero-users-at-migration
   case, and AC-3 covers the HTTP path as well as the background one (it is
   marked `[unit]` today).

2. **§2.2 reproduces a false claim from migration 018's comment.** The comment
   says "the cron iteration queries explicitly filter `WHERE user_id IS NOT NULL
   AND auto_schedule_enabled`". They do not. `ListUsersWithAutoSchedule`
   (`backend/storage/settings.go:379-384`) filters only on
   `auto_schedule_enabled = TRUE` and a non-empty cron. `UserScheduleConfig.UserID`
   is a bare `uuid.UUID` (`:373`), so a NULL `user_id` fails the scan, the
   function returns an error, and `FocusCron.Reload` (`backend/scheduler/cron.go:37-41`)
   logs it and registers **no entries at all**. 018's stated safety argument for
   not tightening the column is therefore untrue, which matters because §2.2 is
   the section the migration decision rests on.
   *Resolves it*: correct the quotation's status in §2.2 — quote it and say it is
   false — and fold the consequence into §2.4.

3. **OQ-1 copies auto-scheduling to every user, and nothing in the spec notices
   what that does on release day.** The partition table puts "Compression and
   auto-schedule enablement and schedule" in the *copied* column. Combined with
   finding 2, the moment migration 024 gives every user a row with
   `auto_schedule_enabled` and `auto_schedule_cron` copied from the shared row,
   `ListUsersWithAutoSchedule` returns every user and `FocusCron` registers a job
   for each. At the owner's chosen cron time the engine writes focus blocks into
   **every user's real calendar**, for users who never enabled it. §6 ("what must
   not change") says only that jobs must not *start failing*; it does not say
   they must not start *acting*. §7 does not mention it either.
   *Resolves it*: either move auto-schedule enablement (not the cron expression)
   to the not-copied column, or add an acceptance criterion in the form "no
   background job acts on a user who had not themselves enabled it", and state
   the consequence in §7.

4. **AC-7 is unsatisfiable against the contract as it stands.** AC-7 requires
   that supplying an empty value of a setting's type clears it. The contract's
   `SettingsFields` gives `workStart`, `workEnd`, `lunchStart`, `lunchEnd` and
   `recapSendTime` the pattern `^([01][0-9]|2[0-3]):[0-5][0-9]$`, which rejects
   `""` — while `validateSettings` (`backend/api/handlers_settings.go:75`) skips
   the format check precisely when the value is empty, and `getLunchWindow`
   (`backend/storage/settings.go:140-142`) treats an empty `lunchStart` as a
   meaningful "no lunch" state. This is a spec/contract conflict that has to be
   settled at one of the two stages, not discovered at stage 4. Detail and the
   schema exit are in my contract review (finding C1).
   *Resolves it*: either AC-7 exempts the clock properties and says why, or the
   contract admits the empty string as it already does for `DaySchedule`.

### Non-blocking

1. **§2.2 "Migration 018 did two things and no more" — it does three.** The third
   is `UPDATE personal_calendars SET user_id = (SELECT id FROM users ORDER BY
   created_at LIMIT 1) WHERE user_id IS NULL` (`:26-30`), a blanket reassignment
   of every unattributed personal calendar to the earliest user with **no**
   unique constraint to stop it — the one hazard §2.2 warns about, applied to a
   table where nothing catches it. The spec moves this table's siblings (§2.5)
   and never mentions it.
2. **AC-2 is sequential-only and GET is now a write.** "A second read returns the
   same document rather than creating a further one" does not constrain two
   *concurrent* first reads by the same user, which now race on
   `settings_user_id_unique` (018:21-22). Worth one clause, because the contract
   gives `getSettings` no 409 and no 400.
3. **§8 OQ-1's "preferences vs credentials" line does not survive contact with
   all 50 fields.** Named, as asked: `awsProfile` is in the copied column as a
   "non-secret provider endpoint", but it names a local AWS credential profile —
   it is a credential by reference, not a preference. `gcpProject` and
   `gcpLocation` are billing targets. `azureEndpoint`/`azureDeployment` identify
   a tenant's resource. Copying these to every user hands everyone the owner's
   spend target with none of the owner's credentials, which is the worst of both
   options the spec considers. Conversely `webcalUrl`, described as "the external
   calendar URL", is a capability URL — a secret in URL form — and is correctly
   *not* copied, but the spec's wording treats it as an ordinary preference.
   The clean partition is by *who the value authorises or identifies*, and on
   that test the three LLM endpoint groups move to the not-copied column.
4. **What a user experiences on release day, stated plainly, is missing from
   §3.** Under the current partition a non-owning user opens Settings and finds:
   their working day, focus and buffer values intact; no LLM key and no
   `llmApiKeySet`; provider still set to (say) `openai`, so every language-model
   feature returns "LLM not configured" or a 401 from the provider; conferencing
   silently back to `meet`; calendar showing disconnected. That is a coherent
   state but it is not "most things kept" — it is "everything that costs money or
   reaches another system is off, with no message". OQ-4 asks whether users are
   told; §3 should describe the state they are told about.

### Criteria I could not falsify

- None. Each of AC-1 to AC-20 names a distinguishable outcome, and §2.11's
  inventory of what exists today is accurate — I checked
  `backend/storage/settings_test.go`, `handlers_settings_test.go`,
  `handlers_settings_schedule_test.go` and `testhelpers_test.go:14-36`.
  AC-16 is the weakest, not because it cannot fail but because it is the only
  guard on a marker resolved without observation (§2.10 U-19); it must not be
  allowed to become optional.

### Citations I checked and found wrong

- §2.2, "the cron queries filter `user_id IS NOT NULL`" (quoted from 018's
  comment) — they do not; `backend/storage/settings.go:379-384`. See Blocking 2.
- §2.2, "Migration 018 did **two** things and no more" — three;
  `018_per_user_settings_and_calendars.up.sql:26-30`. See Non-blocking 1.
- §2.4, "Focus-time scheduling is broken for every user but the first" —
  understated; on a deployment with no users at migration time it is broken for
  all of them, and it is reachable over HTTP, not only from cron. See Blocking 1.

Everything else I sampled was exact: `settings.go:227` / `:262` / `:290-294` /
`:479` / `:501-507` / `:146`; `handlers_settings.go:42-43`, `:58`, `:67-120`;
`microsoft_oauth.go:57,62`; `conferencing.go:5-8`; `personal_calendars.go:154-160`;
`daily_recap.go:72,124-127`; `handlers_llm.go:16-21`; `focus_time.go:76,82-84`;
`auto_decline.go:66,69-71`; `e2e/seed/seed.sql:79-90`;
`smart-calendar-flow/src/pages/Settings.tsx:293,712`;
`src/api/client.ts:526-547,550-563,1141-1143`.

**§2.7's counts, re-derived independently and confirmed exact.** Parsing the
`json:` tags on `storage.Settings` gives 53 tags, 3 of them `-`, so **50**
exposed keys. The frontend interface (`src/api/types.ts:48-89`) declares **34**
properties. `normalizeSettings`/`settingsRequestBody` bridge **4**
(`outOfHoursMeetingsPerWeek`, `autoDeclineOutsideWorkingHours`, `workingHours`,
`lunchBreaks`). Exactly **1** (`timezone`) is spelled identically. **3** have no
backend counterpart — `calendar_id`, `default_conference_provider` (the backend
key is `conferencingProvider`, not a case variant) and `teams_enabled` — leaving
34 − 4 − 1 − 3 = **26** unbridged case-only mismatches, and 50 − 31 = **19**
backend keys with no frontend counterpart. The author's six numbers are all
correct and the audit's "about 4 of ~48" is the one that is wrong.

### Consumers this breaks

- `smart-calendar-flow/src/pages/Settings.tsx:293,712` and
  `src/api/client.ts:526-563` — the 26 unbridged names. Already the subject of
  AC-20; naming it here so the frontend stage cannot treat it as new scope.
- `smart-calendar-flow/src/api/client.ts:1141-1143` — `llmTest` posts the raw
  snake_case `Settings` object to `/llm/test`. Under the new `LLMTestRequest`
  every property is unknown, so the probe becomes a 400 rather than a silent
  test of the wrong configuration. Intended, and it should be said in §5.
- `mcp/tools.go:236` (`clockwise_get_settings`) and `:243`
  (`clockwise_calendar_status`) — both pass the JSON through unchanged
  (`mcp/client.go:22`), so neither breaks. Worth stating in the spec that the
  MCP server has been a third disclosure channel for `llmApiKey`, because §1
  says "any signed-in account" and this one is a model.
- `backend/scheduler/cron.go:37-56` — see Blocking 3. This is the consumer the
  spec does not name and the one that acts on release day.
- No e2e consumer: `e2e/tests/` has no settings spec, and
  `e2e/seed/seed.sql:79-90` already uses `ON CONFLICT (user_id)`.
