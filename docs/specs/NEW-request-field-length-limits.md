# Spec — NEW-request-field-length-limits: no request field has a maximum length, so one request can store a megabyte in a team name, a link title, a booker's name or a booking note

- **Linear**: **[PAC-164](https://linear.app/paceday/issue/PAC-164/security-no-request-field-has-a-maximum-length-so-one-request-can)** — "Security: no request field has a maximum length, so one request can store a megabyte in a team name, link title, booker name or booking note". Filed 2026-11-22 in Backlog, with the spec linked as an attachment. Filing it claims the id and records the problem; it does not commit the owner to the work, which is still **awaiting approval**.
  - **Rename this file to `docs/specs/PAC-164.md` on approval.** It keeps the `NEW-*` name only because the directory's convention is that an `NEW-*` spec is one the owner has not accepted yet (`NEW-booking-rate-limit.md` §Linear is the precedent).
  - **Why the number was filed late, and the convention that follows** (recorded because it cost a collision). Revision 1 deliberately claimed no number: it reasoned "PAC-162 is the highest, PAC-163 is next free, but another session may file first". One did — PAC-163 is an unrelated `Retry-After` CORS change, filed about an hour later. The instinct was right (an unapproved document should not squat an id) but the execution left a stale reference in a document people were already reading. **The convention now: filing the issue is the only thing that claims a number, so a spec either files and holds one, as this one now does, or names none at all. "Next free is N" written into a document is neither.**
- **Status**: draft, **revision 3** (awaiting spec-challenger). **It argues against part of its own brief** — see §0.
  - **Revision 3 (2026-11-22)** claims the Linear id. PAC-164 is filed and the header above records it; the revision-1 reasoning about PAC-163 being "next free" is gone, because that number now belongs to an unrelated change. The §4 test file is named `pac164_test.go` rather than a placeholder. No decision, criterion or limit changes.
  - **Revision 2 (2026-11-22)** reconciles the spec with what the `request:mail-injection-and-public-booking-enum` session has actually built (§2.8, reported but **[unverified-peer]** — not read in that worktree). Three changes: address fields are declared and counted in **bytes**, not code points, to match RFC 5321 and that session's shipped `maxAddressLen` (D-7 revised; §3.1, §3.8 #4, #6, #11, AC-3, new AC-3a and AC-3b, and `octet_length` in §7.1); `POST /api/book/{slug}` `email` leaves this spec's scope entirely, because that session shipped both halves of SF-005 under one message, so this spec adopts theirs (D-9 revised; §3.8 #13, Inherited scope, §5, §7.2); and **new D-15** judges their render-boundary cap against D-2 — consistent, with one carve-out this spec calls a defect in the shipped form.
  - Revision 1 was the original draft.
- **Author**: spec-author (factory), in a CodeDen session on branch `codeden/spec-enforce-contract-validation-on-aea8`.
- **Verified against**: clockwise-like `origin/main` `c3d23aa` ("chore(factory): version the merge scripts; CodeDen sessions (#330)"). Every backend, contract, migration and seed citation below was read at that SHA.
  - **smart-calendar-flow was NOT available to this session.** Every claim about what the web app sends is marked **[unverified-web]** with the file a reader must open in that repo to settle it. There are three such claims and no more.
- **Repositories touched**: clockwise-like (contract, backend, tests). smart-calendar-flow regenerates its types; whether it also needs form-level `maxlength` attributes is **[unverified-web]** and is an open question (OQ-3), not an assumption.
- **Process**: full. A documented answer changes on nine operations (a new 400 or 422 appears), and stored rows may violate a limit the server did not have when they were written (§3.5, §7.1).
- **Inherited scope** (`docs/factory/README.md` §7 rule 1, "touch it, fix it"). This work modifies the request half of `createTeam`, `renameTeam`, `inviteTeamMember`, `addTeamPerson`, `createSchedulingLink`, `updateSchedulingLink`, `createPublicBooking`, `createPersonalCalendar` and `updatePersonalCalendar`, so the open findings on those operations are in scope:
  - **SF-005** (`docs/factory/silent-failures.md:147`, high, confirmed, **closed by the mail-injection work, revision 1**): `createPublicBooking` accepted any non-blank booker address; an invalid one was stored and answered 201, and the host calendar event and the confirmation email then failed where only Sentry saw it. **Resolution (D-9, revised): nothing is left for this spec to do on that field.** The `request:mail-injection-and-public-booking-enum` session has shipped both halves on `POST /api/book/{slug}` `email` — an "exactly one address" shape check *and* `maxAddressLen = 254` **bytes** — both answering `400 {"error":"email must be a valid email address"}` (§2.8). That is this spec's proposed bound, in the right unit, with a message already chosen, so this spec adopts theirs and declares no second rule (§3.8 #13). SF-005 is re-marked **"Fixed (mail-injection work)"**, not "partly addressed", and this spec is cited only as the place the unit question was settled.
  - **SF-043** (`silent-failures.md:189`, medium, likely, **open**, paired with API-075): personal calendars accept any `provider` string and an unchecked URL that can never sync. **Resolution (D-10): the `provider` half is in scope** (a 40-character bound; the allow-list is still not added, and that is named out of scope in §5). The **URL half is explicitly not in scope** — it is the in-flight webcal work's (§2.8). The row is re-marked, not closed.
  - **API-075** (`docs/factory/api-audit.md:345`, low, **Reported**): `createPersonalCalendar`'s `enabled` is a plain `bool`, so an absent key creates a *disabled* calendar. **Resolution: not fixed here, and that is a deliberate refusal** (§5). Changing it changes what an existing client's request *does*, which is a different decision from refusing a request outright, and no acceptance criterion here asks for it. The row stays Reported with a note pointing at this spec for why it was passed over.
  - **API-097** (`api-audit.md:367`, low, **Confirmed, not fixed**): the body cap's 400 is documented once in `info.description` and on no operation. This spec adds a *second* undocumented-answer risk on the same nine operations, so it is in scope: every new refusal is declared on its own operation (AC-21), and the contract stage states the body-cap 400 on those nine at the same time. The row is narrowed to the operations this spec does not touch.
  - `x-uncertain`: **none** on any operation in scope. The two markers in `contracts/openapi/paths/teams.yaml:2540` and `:2547` are on `NoMeetingZone.startTime`/`endTime`, and the one in `habits.yaml:499` is on a `{event_id}` branch. Checked by reading every `x-uncertain` in `contracts/openapi/paths/` at `c3d23aa`.
  - **Read and set aside** (Fixed, behaviour preserved by §6): API-006, API-048, API-049, API-050, API-051, API-053, API-094, API-095, API-096, SF-008, SF-027, SF-028, SF-029, SF-066.

Decision marks: **[F]** = factory decision, recorded here and open to challenge. **[OQ]** = open for the owner, with the default the spec is written to.

---

## 0. The brief's premises, checked

This spec was commissioned to close "a root cause behind one HIGH and three LOW findings: the hand-written handlers decode request bodies with `json.Decoder` instead of the generated strict wrapper, so the contract's `maxLength` and pattern constraints are never enforced at runtime." That audit is not in this repository (`docs/factory/api-audit.md` and `docs/factory/silent-failures.md` carry no such row at `c3d23aa`), so its finding ids could not be checked. Its premises were checked against the code, and **three of the four do not hold.**

| Premise | Verdict |
|---|---|
| The hand-written handlers decode bodies straight with `json.Decoder`. | **Confirmed.** Every one of them does. In the handlers this spec touches: `handlers_teams.go:53`, `:173`, `:224`, `:481`, `:527`; `handlers_scheduling_links.go:521`, `:805`, `:1061`; `handlers_booking.go:408`; `handlers_personal.go:75`, `:112`; `handlers_manager.go:88`, `:197`, `:328`, `:431`, `:523`. Elsewhere: `handlers_habits.go:95`, `:197`, `:358`; `handlers_schedule.go:96`, `:150`, `:187`; `handlers_freebusy.go:46`; `handlers_nlp.go:23`, `:43`; `handlers_events.go:173`; `handlers_sso.go:39`; `handlers_focus.go:30`. |
| Going through the generated strict wrapper would enforce `maxLength` and `pattern`. | **False.** The generated strict handler decodes with **the same call**: `json.NewDecoder(r.Body).Decode(&body)` (`backend/api/gen/paceday.gen.go:24764`, inside `strictHandler.AddTeamPerson`; every other `strictHandler` method has the identical line). The generated request type's `UnmarshalJSON` (`:5847-5882`) only sorts known keys from `AdditionalProperties`. There is **no** `maxLength`, `minLength` or `pattern` check anywhere in `paceday.gen.go`: every one of those keywords survives only inside a doc comment. No `openapi3filter`/kin-openapi validation middleware is mounted (`backend/api/routes.go`, `middleware.go`). Adopting the wrapper would change the 400 *message* on a type error and nothing about constraints. |
| "The contract documents limits the server does not apply." | **False for every field named in the brief.** The whole bundle contains **six** `maxLength` keywords (`contracts/openapi/openapi.yaml:3688`, `:12977`, `:13036`, `:13211`, `:18685`, `:18860`) and **all six are enforced** — see §2.3. None of them is on a team name, a link title, a booker name, a note, or a personal-calendar label or URL. The contract states the *absence* in so many words: `TeamNameRequest.name` carries "**No maximum length is enforced by the backend**" (`:17907-17915`), `CreatePublicBookingRequest.notes` carries "**Free text; no length limit enforced**" (`:12118-12120`), `PersonalCalendarCreateRequest.provider` carries "Any non-empty string is accepted; there is no allow-list" (`:14796-14817`). That is `docs/factory/README.md:345-347` rule 2 working as designed: "**the contract describes what is, not what should be.**" There is no contract-to-runtime divergence to close. |
| "`AddTeamPerson` already goes through the generated strict wrapper and is the model to follow." | **Confirmed that it does** (`contracts/openapi/MIGRATION.md`, row `addTeamPerson`; `handlers_team_people.go:46`), **and it is the counter-example.** It applies no length check to `name` or `email` (`:51-66`): a one-megabyte `name` is accepted, stored in `team_added_members.display_name` and returned in every team read. The strict wrapper did not help it. |
| `inviterName` is a request field of `createTeam`. | **False.** `inviterName` is a *response* field of `getTeamInvite` (`openapi.yaml:17791`) read from `users.name`, which is written **only at sign-in from the OAuth identity provider's profile** (`backend/storage/signin.go:198`, `:263`). No request body in this API writes it. It cannot be bounded by body validation, and it is named out of scope in §5. |

**What survives, and is worth doing.** The *harm* the audit describes is real and is confirmed below: there is exactly **one** field-length check in the whole backend (`handlers_habits.go:36-37`), the only other bound is the 1 MiB body cap (`middleware.go:411-412`), and roughly a megabyte per field is therefore storable and reaches email subject lines, ICS attachments and list responses. But the work is **not** "enforce the contract". It is "**decide limits that do not exist yet, declare them in the contract, and enforce them**" — a product decision with a contract change and a compatibility cost, not a bug fix. Calling it a bug fix would hide the decision the owner is actually being asked to make, and would invite an implementer to "just route it through the wrapper", which would change nothing.

**One part of the brief this spec declines.** It does not propose migrating any operation to the generated strict wrapper. That migration is a real debt (`MIGRATION.md`, 14 of 134 operations), it is one-way and gated, and every row's `notes` column records a specific behaviour change it would cause. Bundling it with a validation change would make the validation change unreviewable, and it would buy this spec nothing (premise 2). The register's own rule — move a row when the operation is *otherwise* being modified, deliberately — is the right home for it.

## 1. Problem

A single authenticated request, and for one endpoint a single **anonymous** request, can store roughly a megabyte of text in a field meant to hold a short label. Nothing stops it: no field in any request body has a maximum length, no column has one, and the only ceiling is the 1 MiB whole-body cap.

Who pays, concretely:

- **A host whose public booking page is abused.** `POST /api/book/{slug}` needs no authentication. A booker's `name` goes into the confirmation email's body and into the ICS attachment's `ATTENDEE;CN=` line; the link's `title` goes into the email's `Subject:` header and the ICS `SUMMARY:` line. A megabyte in either produces a mail nobody's client renders and an ICS file no calendar accepts, and the host sees a booking they cannot read in their own list. The booking row is saved first, so the damage is stored before any of it is attempted.
- **Everyone reading a list.** `notes`, `booker_name`, team names and link titles are returned in list responses with no truncation, so one planted row inflates every later read of that list for its owner — a stored cost paid repeatedly by someone who did not cause it.
- **Whoever is billed for the language model.** `POST /api/nlp/parse` forwards its `text` field to the configured provider with no bound, so the request body cap is also the prompt-size cap.
- **A link owner, through an answer nobody can act on.** `scheduling_links.slug` carries a unique btree index. A slug over roughly 2700 bytes makes the insert fail with SQLSTATE `54000`, which is not the `23505` the handler translates, so the caller gets a **500** where the honest answer is a refusal. The same holds for every indexed address column.

What makes this worth a spec rather than a patch: the limits do not exist anywhere yet, these are live endpoints with real stored data, and a limit introduced carelessly makes an existing record **un-editable through its own endpoint**. That has already happened once in this codebase, unnoticed — see §2.7.

## 2. Current behaviour

### 2.0 Names used in this spec

| Name | What it is today |
|---|---|
| **The body cap** | `bodyCapMiddleware(1 << 20)`, `backend/api/middleware.go:411-440`, wired in `routes.go`. A declared `Content-Length` over 1 MiB, or a body that reads past it, is `413 {"error":"request body too large"}` before any handler; a read failure is `400 {"error":"invalid request body"}` (API-097). A body of exactly the cap is passed on. |
| **The strict wrapper** | `gen.NewStrictHandlerWithOptions` and the `strictHandler` methods in `backend/api/gen/paceday.gen.go`, with `strictOptions` at `handlers_credentials.go:62-70`. Fourteen operations are mounted through it (`contracts/openapi/MIGRATION.md`). |
| **The one existing limit** | `validateHabitFields`, `backend/api/handlers_habits.go:36-37`: `len([]rune(strings.TrimSpace(title))) > 200` is `400 {"error":"title must be 200 characters or fewer"}`. |
| **The address normaliser** | `storage.ValidAddress` over `storage.TrimAddressSpace` (`backend/storage/address_trim.go:28-55`), reached through `api.normalizeManagerEmail` (`handlers_manager.go:870-879`). Trim, lowercase, `net/mail.ParseAddress`, parsed address must equal the input. **No length bound at any step.** |
| **The merged-record rule** | `updateSchedulingLink` merges the patch onto the stored row and re-validates **the whole merged record** (`handlers_scheduling_links.go:810-882`, `validateSchedulingLink(&updated)` at `:878`). `updateHabit` does the same with `validateHabitFields`. Documented at `openapi.yaml`, `updateSchedulingLink`: "The whole merged record is re-validated, so a patch can fail 422 on a field it did not touch." |
| **The carried-only precedent** | The `slugPatched` flag (`handlers_scheduling_links.go:811`, `:816`, `:875-878`): the empty-slug 422 fires only when the request *carried* `slug`, so a link is never refused for a stored value it did not send. This is the shape §3.3 generalises. |

### 2.1 The strict wrapper decodes; it does not validate

`strictHandler.AddTeamPerson` (`gen/paceday.gen.go:24758-24788`):

```go
var body AddTeamPersonJSONRequestBody
if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
    sh.options.RequestErrorHandlerFunc(w, r, fmt.Errorf("can't decode JSON body: %w", err))
    return
}
```

Every other `strictHandler` body method is byte-identical in this respect. What the wrapper adds over a hand-written decode is exactly two things: a typed request object, and a uniform `400 {"error":"invalid body"}` for an undecodable body (`strictOptions.RequestErrorHandlerFunc`, `handlers_credentials.go:64-66`). What it does not add: length, pattern, enum, `minLength`, `required` or `format` checks. `grep -c` over `paceday.gen.go` finds no executable use of any of those keywords; they appear only inside generated doc comments, several of which say so themselves — `openapi.yaml:5045` on `WorkingHours.days`: "no generator carries `propertyNames` into a validator, **so the server's exact-match check is the enforcement point**"; `:3934` on `SettingsUpdate`: "These are enforced by the handler, not by a generated validator."

### 2.2 `AddTeamPerson`, the proposed model, has no length check

`handlers_team_people.go:46-84`. `name` is read at `:55-57` and passed straight to `storage.AddTeamPersonCtx` at `:66`. `email` goes through the address normaliser at `:62`, which has no length bound. A 1 MiB `name` is stored in `team_added_members.display_name` (`migrations/041_team_added_members.up.sql:21`, `TEXT`) and returned by every team read. A 1 MiB `email` is **worse**: `team_added_members` has `PRIMARY KEY (team_id, email)` and `CREATE INDEX team_added_members_email ON team_added_members (email)` (`:25`, `:28`), so past roughly 2700 bytes the insert fails SQLSTATE `54000` and the caller gets the operation's 500, not a refusal.

### 2.3 Every constraint the contract declares on a request, and whether it is enforced

Six `maxLength` keywords, four `pattern` keywords on request shapes. All of them are enforced, every one by a hand-written check:

| Where | Constraint | Enforced? |
|---|---|---|
| `getPublicBookingSlots` query `display_time_zone` (`openapi.yaml:3688-3690`) | `maxLength: 64`, `pattern` | **Yes**, by the handler (PAC-88); pinned by `backend/api/pac88_listing_test.go:329`, `:337`, which reject a 65-character name. |
| `HabitRecord.title`, `HabitCreateRequest.title`, `HabitUpdateRequest.title` (`:12977`, `:13036`, `:13211`) | `maxLength: 200` | **Yes**, `handlers_habits.go:36-37`. The only field-length check in the backend. |
| `BookingIdempotencyKey`, `SchedulingLinkCreateIdempotencyKey` headers (`:18685`, `:18860`) | `minLength: 16`, `maxLength: 255`, `pattern` | **Yes**, by the handler; pinned by `pac82_test.go:645-646` and `pac119_test.go:688`, which reject 15 and 256 characters. |
| `CreateConferenceRequest.title` (`:12068`) | `pattern: \S` | **Yes**; `openapi.yaml:3943` records that pattern and handler "accept and refuse the same titles". |
| `co_host_emails` items, create and PATCH (`:12277`, `:18200`) | `minLength: 1`, `pattern: \S` | **Yes**, `blankCoHostEntry` → 422 (PAC-112). |
| Settings and habits clock fields (`:12447` and fourteen others) | `HH:MM` patterns | **Yes**, by handler shape checks plus `time.Parse` (PAC-24). |

So **no declared constraint is unenforced**, and `HabitTemplate.window_start`/`window_end` (`:13188-13193`) are the only place contract and server disagree — and there the contract is *looser* than the server (`^[0-9]{2}:[0-9]{2}$` admits `99:99`, which `validHabitClock` rejects). That is the opposite of the reported defect and is named out of scope in §5.

### 2.4 Every request field that has no length bound and is stored

Read from each handler's anonymous body struct at `c3d23aa`. "Normalisation" is what the field already gets before storage.

| Operation | Field | Handler | Normalisation today | Column |
|---|---|---|---|---|
| `createTeam` | `name` | `handlers_teams.go:50-57` | `strings.TrimSpace`; blank → 400 "name is required" | `teams.name TEXT` (`013:4`) |
| `renameTeam` | `name` | `:170-176` | same | same |
| `inviteTeamMember` | `name` | `:221-227` | none at all | `team_added_members.display_name TEXT` (`041:21`) |
| `inviteTeamMember` | `email` | `:224` | address normaliser | `team_invites.invitee_email`, `team_added_members.email` — **indexed** |
| `addTeamPerson` | `name` | `handlers_team_people.go:55-57` | none | `team_added_members.display_name` |
| `addTeamPerson` | `email` | `:62` | address normaliser | as above — **indexed** |
| `createSchedulingLink` | `title` | `handlers_scheduling_links.go:223`, `:252-254` | `TrimSpace`; blank → 422 "title is required" | `scheduling_links.title TEXT` (`009:5`) |
| `createSchedulingLink` | `slug` | `:169-182`, `:223` | `normalizeSlug`: lowercase, `[a-z0-9]` kept, runs of anything else → one `-`, trimmed. **No truncation.** | `scheduling_links.slug TEXT NOT NULL UNIQUE` (`009:4`, index `009:47`) |
| `updateSchedulingLink` | `title`, `slug` | `:810-817` | as create | as create |
| create/PATCH | `co_host_emails[]` | `blankCoHostEntry`, `setCoHostList` | ECMA trim, `lower()`, `net/mail` | `scheduling_link_hosts.invitee_email` — **indexed** (`044:106`) |
| `createPublicBooking` | `name` | `handlers_booking.go:399-412` | `TrimSpace`; blank → 400 | `bookings.booker_name TEXT` (`009:29`) |
| `createPublicBooking` | `email` | `:400`, `:411` | `TrimSpace` only; **never parsed** (SF-005) | `bookings.booker_email TEXT` |
| `createPublicBooking` | `notes` | `:403` | none | `bookings.notes TEXT` (`009:34`) |
| `createPersonalCalendar` | `provider` | `handlers_personal.go:69`, `:78-81` | non-empty → 400 "provider required"; no allow-list | `personal_calendars.provider` |
| `createPersonalCalendar` | `name` | `:70`, `:82-84` | empty → literal `"Personal"` | `personal_calendars.name TEXT` (`004:4`) |
| `createPersonalCalendar` | `url` | `:71` | none | `personal_calendars.url TEXT` (`004:5`) |
| `updatePersonalCalendar` | `name`, `url` | `:104-108` | none; SQL `COALESCE` | as above |
| `addManagerTeamMember` | `display_name` | `handlers_manager.go:324`, `:343` | `TrimSpace` | `manager_team_members.display_name TEXT` (`016:14`) |
| `patchManagerTeamMember` | `display_name` | `:427` | none | same, plus `display_name_override` (`023:2`) |

Every one of those columns is `TEXT` with no check constraint. Nothing in the database bounds a value's length except the btree index entry limit on the columns marked **indexed** (roughly 2704 bytes), which produces a 500 rather than a refusal because `isUniqueViolationOn` matches only SQLSTATE `23505` on a named constraint (`backend/storage/scheduling_links.go:940-943`).

Unbounded and **not** stored (named out of scope in §5): `patchEvent`'s `title`, `description`, `location`, `attendees[]` (`handlers_events.go:149-158`), which go straight to Google; `parseNaturalLanguage`'s `text` (`handlers_nlp.go:19-24`), which goes to the language-model provider; `getCalendarFreeBusy`'s `emails[]` (`handlers_freebusy.go:41-58`, already capped at 20 entries but each entry unbounded).

### 2.5 Where an over-long value goes

- **Email subject lines.** `Subject: Confirmed — <link title> on <date>` (`backend/engine/booking.go:845`); `"%s invited you to join the \"%s\" team on Paceday"` with the inviter's display name and the team name (`handlers_teams.go:806`). A header is a single unfolded line here.
- **The ICS attachment.** `generateICS` (`engine/booking.go:874-893`) concatenates `SUMMARY:` + link title, `DESCRIPTION:Meeting with ` + host names, and `ATTENDEE;CN=` + booker name + `:mailto:` + booker email, with no escaping and **no RFC 5545 line folding**.
- **The SMTP envelope.** The booker's address becomes the `To:` header and the `RCPT TO` recipient (`engine/booking.go:869`); the invitee's address likewise (`handlers_teams.go:788-812`). RFC 5321 caps a forward path at 256 octets, so a longer address is a protocol violation this server emits.
- **A generated calendar-event title.** `q.Set("title", fmt.Sprintf("1:1 with %s", m.DisplayName))` (`handlers_manager.go:540`).
- **List responses.** `notes` is returned by three response schemas (`openapi.yaml:11246`, `:14963`, `:15797`); link titles, team names, booker names and personal-calendar labels are all returned untruncated.
- **A public URL path segment.** A stored slug is the public booking address, `/book/{slug}`.

### 2.6 What the stored data looks like, as far as this repository can show

- **`e2e/seed/seed.sql`**: every value in every column named in §2.4 is short. Longest link title `'E2E PAC-40 Archived Link'` (24 characters), longest booker name `'E2E PAC-40 Cancelled Booker'` (27), longest team name `'E2E Team Detected'` (17), longest personal-calendar label `'E2E Seam Personal'` (17), longest note `'seeded — PAC-40 archive fixture'` (31). No seeded value exceeds 40 characters.
- **Migrations**: no migration has ever written, backfilled or truncated any of these columns with a long value. The only migration using `substr`/`left` is `043_org_membership_rule.up.sql`, on a domain expression.
- **Production**: **unverified, and unverifiable from here.** §7.1 is the query that settles it, and it is the primary gate.

### 2.7 Tests that pin today's behaviour — and the precedent that already went wrong

- **No test anywhere asserts that an over-long body field is accepted.** `grep -rn "strings.Repeat" backend/api/*_test.go backend/storage/*_test.go` at `c3d23aa` returns six hits, and every one is on a field that *is* bounded (`pac82_test.go:645-646`, `pac119_test.go:688` on the idempotency key; `pac88_listing_test.go:329`, `:337` on the time zone) or is an unrelated response pad (`pac89_stepup_test.go:255`). So nothing protects today's unbounded acceptance. That absence is itself a finding: the behaviour this spec changes was never a decision, it was a gap.
- **The precedent already failed once, silently.** `habits` shipped on 2026-04-26 (`02b3696`, `migrations/010_habits.up.sql`). The 200-rune title check arrived on 2026-08-18 (`4cfbdda`, `handlers_habits.go:36-37`) **with no migration and no backfill**. `updateHabit` re-validates the merged record, so any habit created in that window with a longer title cannot be edited at all today: every PATCH of it, including `{"active": true}`, answers `400 {"error":"title must be 200 characters or fewer"}` for a value the request never sent. Nobody noticed, because nothing counted the rows. This is exactly the failure mode §3.3 exists to prevent, and it is in scope to repair (AC-19).
- Tests that must keep passing are listed in §6.

### 2.8 What the two in-flight sessions cover, and where the line is

Two other sessions are editing handler code against this repository on 2026-11-22. Their work and this spec meet on three fields, and the boundary is **character class and shape** versus **length**:

| Work | Covers | This spec does **not** |
|---|---|---|
| `request:mail-injection-and-public-booking-enum` — **as reported on 2026-11-22, uncommitted in its worktree; verified only from that report, not read here [unverified-peer]** | **(a)** Control-character rejection at the entry path: CR, LF and NUL in a name are a 400; in a link title, `422 title must not contain line breaks`. **(b)** `POST /api/book/{slug}` `email` gains an "exactly one address" shape check **and** `maxAddressLen = 254` **bytes**, both `400 {"error":"email must be a valid email address"}` — their only entry-path length check. **(c)** A render-boundary guard, `safetext.MaxLen = 200` **runes** per interpolated value, applied **only inside the mail and ICS composers**; no request is refused for length by it and the stored value is untouched. **(d)** `GenerateSlug` always appends a random tail — `{first-name}-{duration}min-{8 lowercase hex}`, 4 bytes from `crypto/rand`, redrawn on collision up to 10 times, a failed `rand.Read` an error rather than a predictable suffix, with a `slugBase` fallback to the stem `"meet"` for an empty name. The client-supplied slug path is untouched. | Re-specify any character-class rule on `booker_name`, `booker_email`, link `title`, team `name` or the inviter's display name; add any rule of its own to `POST /api/book/{slug}` `email` (§3.8 #13 is now **theirs**, D-9); or restate the composer guard. **Two consequences of their change this spec must carry:** (i) their (c) and this spec's title/name limits are the *same number at two different boundaries*, and the layering is deliberate — see D-15; (ii) their (d) builds a generated slug from the owner's display name, which no request bounds (§5), so a generated slug can exceed this spec's 80-character request limit. That is sound, because the limit is declared on the **request** property and a generated slug is not a request value (D-5's reasoning, applied to the slug); §3.8 #9 says so explicitly. |
| `request:webcal-ssrf-egress-guard` | Validating the personal-calendar subscription URL: scheme, host, egress. | Constrain the **content** of `url`. It bounds its **length** only (D-10), and says so in the contract so the two rules read as one field with two independent checks. |

Length and character class are independent: rejecting CR/LF does not bound a megabyte of letters, and bounding length does not stop a two-line value. Neither work makes the other unnecessary. A reader of either diff should find the other named.

## 3. Desired behaviour

### 3.1 Every short-label request field has a declared maximum, and the server enforces it [F, D-1]

Each field listed in §2.4 gains a maximum length. The contract declares it, the server refuses a longer value, and the two are the same number by construction — the house pattern the habit title already follows, and the only pattern that works, since no generator enforces the keyword (§2.1).

Lengths are counted in **Unicode code points, after the trim the field already applies today** (§2.4's "Normalisation" column), so the limit composes with existing normalisation instead of competing with it. For the slug the count is taken **after** normalisation, because that is what is stored, what must fit the index, and what becomes a URL.

**Addresses are the one exception: they are counted in bytes** (D-7, revision 1). An address limit is not a limit on what a person reads, it is a limit on what fits a wire protocol field, and RFC 5321's 254 is 254 **octets**. The mail-injection work's shipped check uses bytes (§2.8), and declaring a second unit for the same number on the same field would make the contract and the handler disagree on which strings are legal — a 200-code-point address of 4-byte code points is 800 octets and must be refused; a 254-octet ASCII address is exactly at the limit and must be accepted. Every address field in the table below therefore says **bytes**, and only address fields do.

The limits, all **[F]** and all open to the owner (OQ-1):

| Field | Limit | Why this number |
|---|---|---|
| team name (create, rename) | 200 | The habit title's number, already shipped and already in the contract. A team name is a short label. |
| link title | 200 | Same. It is also the email `Subject:` and the ICS `SUMMARY:`. |
| link slug (after normalisation) | 80 | It is a URL path segment a person types or reads aloud. Well inside the index limit with three orders of magnitude of headroom. |
| booker name | 200 | The ICS `ATTENDEE;CN=` and the email greeting. |
| booker email | **254 bytes** | **Already shipped** by the mail-injection work as `maxAddressLen = 254` bytes (§2.8). Listed here for completeness and as the unit precedent the three rows below follow; this spec adds nothing to this field (§3.8 #13). RFC 5321's maximum forward path, in octets. |
| booking notes | 2000 | The only field here that is genuinely prose. Ten times the longest label and still three orders below the body cap. |
| personal-calendar label | 200 | A list label. |
| personal-calendar `url` | 2048 | The de-facto browser and proxy URL ceiling. |
| personal-calendar `provider` | 40 | It is a provider key (`google`, `outlook`, `webcal`). |
| added-person / invitee display name | 200 | A display label. |
| added-person / invitee email | **254 bytes** | The booker email's number and unit, so Paceday has one address limit rather than two. Also an indexed column. |
| co-host list entry | **254 bytes** | As above. Also an indexed column. |
| manager member display name | 200 | A display label, and a generated calendar-event title. |

### 3.2 A value over the limit is refused, never truncated [F, D-2]

The server refuses. It does not shorten, and it does not accept-and-shorten-quietly. Five reasons, in order of weight:

1. **Truncation cannot be reported.** Every answer in this API is the shared `{error}` envelope or the record. Neither can say "I kept your request but changed your data". A client would get a 201 and a value it did not send, with no way to learn that.
2. **Truncating addresses is dangerous.** A cut address can be *another person's* valid address. A cut URL can be a different host. These are not cosmetic losses.
3. **Truncating text produces mojibake.** A cut at a code-point boundary still splits grapheme clusters; a cut at a byte boundary splits code points. Either is stored for ever.
4. **The house already refuses.** The habit title, the idempotency key and the time zone all refuse, with a message. A truncating field would be the only one of its kind.
5. **Refusal is reversible from the client's side.** The person shortens their input and sends it again. Truncation is not: the original is gone.

### 3.3 A limit is checked only against a value the request carries [F, D-3] — the single most important decision here

A length check applies to the value **in the request**. It never applies to a value the server read back from storage.

This is a deliberate, narrow exception to the merged-record rule (§2.0), and it is the whole answer to "can a tightened limit make an existing record un-editable". Concretely, for a stored row whose value exceeds a new limit:

- a read of it always succeeds, unchanged;
- a write that **does not carry** that field always succeeds — a link with a 5000-character title can still be paused, renamed in another field, or have its co-hosts changed;
- a write that **does carry** that field is refused, with the message, until the carried value is short enough;
- so the one and only way out of an over-long stored value is to send a shorter one, and that path is never blocked.

The `slugPatched` flag (§2.0) is the existing shape of this, in the same handler. Without this decision, §2.7's habits failure repeats on nine more operations at once.

### 3.4 Each operation refuses in its own existing style [F, D-4]

The refusal uses the status code the operation already uses for a bad field, and the message form the habit title already uses — `"<field> must be <N> characters or fewer"`:

| Operation | Code | Why |
|---|---|---|
| `createTeam`, `renameTeam` | **400** | Their only field refusal is the 400 "name is required" (`openapi.yaml`, `createTeam` 400). |
| `createSchedulingLink`, `updateSchedulingLink` | **422** | Every field refusal there is a 422 (`validateSchedulingLink`). |
| `createPublicBooking` | **400** | Every field refusal there is a 400. |
| `createPersonalCalendar`, `updatePersonalCalendar` | **400** | "provider required", "invalid body" are both 400. |
| `inviteTeamMember`, `addTeamPerson` | **422** | Their shape refusals are 422; "required" is their 400. A length violation is a bad value, not a missing one. |
| manager member add / patch | **422** | Their field refusals are 422. |

Inventing one uniform code across nine operations would change each one's existing answer family, which is a larger contract change than the limits themselves and breaks clients that branch on the code.

`addTeamPerson`'s 422 body is a **closed enum** of four exact strings (`gen/paceday.gen.go:73-78`, `AddTeamPersonError`). Adding a message there widens a closed enum and so widens the generated type and the web's zod schema. That is a real consequence the contract stage must decide, not paper over (noted for stage 2; OQ-4).

### 3.5 Response schemas gain no maximum [F, D-5]

The limits are declared on **request** schemas only. No response schema gets a `maxLength`, because stored rows written before the limit are kept and still returned, and a response constraint would make the contract false for them — and would make the web's generated zod schema reject a record the server legitimately holds.

This is not a new judgement. It is the reasoning the bundle already records for `awsRegion` (`openapi.yaml:3777`): "Deliberately no `pattern`: this map is shared with `Settings`, and rows stored before PAC-66 are kept as they are, so a pattern would make the response schema false for them." Same situation, same answer.

### 3.6 No log-only phase; measure the stored rows instead [F, D-6]

The owner was asked whether to ship a log-only release first and enforce later. **The recommendation is no**, and the reason is that log-only answers the wrong question.

- Log-only observes **writes** during an observation window. The risk is **stored rows** — a record that becomes un-editable. A write-time log sees none of them.
- The one query in §7.1 reads every stored row in one pass, over the same production data, in seconds. It answers the real question completely, where log-only answers a proxy of it partially.
- A log-only mode costs a full release cycle, a code path that must later be removed, and a window in which the server has the check and does not apply it — a state nobody can reason about from the contract.
- §3.3 already removes the failure log-only would be protecting against: an over-long stored row is readable and editable.

So the phasing is: **measure, choose limits above what is stored, enforce in one release.** If §7.1 finds rows above a proposed limit, the limit is raised above the observed maximum before release, or the owner decides the rows are not worth keeping — in which case §7.1 becomes a stop condition (OQ-2), not a surprise after deploy.

### 3.7 Compatibility

- A client sending values of ordinary length sees no change at all.
- A client sending a longer value gets a refusal where it got a 201 or 200. §3.8 enumerates every such case.
- Response shapes are unchanged. No field is removed, renamed or re-typed.
- The body cap, the 413 and its 400 are unchanged.

### 3.8 Every currently-accepted payload that starts being rejected

This is the enumerated list the decision rests on. "Accepted today" means the request succeeds at `c3d23aa`. "Stored rows at risk" is whether an *already-stored* value could exceed the proposed limit — the column that matters most, because §3.3 is what keeps it survivable.

| # | Operation and field | Contract today | Accepted today | After the change | Stored rows at risk? |
|---|---|---|---|---|---|
| 1 | `POST /api/teams` `name` | `minLength: 1`; "No maximum length is enforced by the backend" | any non-blank string, up to ~1 MiB | `400 {"error":"name must be 200 characters or fewer"}` | Possible, unverified. No seeded value over 17. §7.1 counts them. §3.3: such a team still renames to a short name, and every other team operation is unaffected. |
| 2 | `PATCH /api/teams/{id}` `name` | same schema | same | `400`, same message | As #1. `name` is the operation's only field, so a team whose stored name is over the limit can only be renamed — which is the one thing it needs. |
| 3 | `POST /api/teams/{id}/members/invite` `name` | no constraint | any string, including a megabyte, no trim | `422 {"error":"name must be 200 characters or fewer"}` | Possible, unverified; `display_name` is never re-sent by a later write, so no record becomes un-editable. |
| 4 | `POST /api/teams/{id}/members/invite` `email` | no constraint; address normaliser only | any address `net/mail` parses. Over ~2700 bytes: **500 today** (index entry too large) | `422 {"error":"email must be 254 bytes or fewer"}` — **bytes, not characters** (D-7). So part of this change turns a 500 into a refusal | Bounded in practice: a stored address over ~2700 bytes could never have been inserted. Between 254 bytes and ~2700 is possible and unverified; the byte unit makes this *stricter* than the code-point version this row carried in the draft, so a non-ASCII address of 100 code points and 300 octets is now refused where the draft would have accepted it. The address is an identity key, not an editable field, so §3.3 does not arise; an over-long stored address stays readable and removable by `DELETE`. |
| 5 | `POST /api/teams/{id}/people` `name` | no constraint | any string | `422`, as #3 | As #3. **Note:** this is the operation already on the strict wrapper; it gains the check by hand, like every other. |
| 6 | `POST /api/teams/{id}/people` `email` | no constraint | as #4 | `422`, as #4, **in bytes** — **but** its 422 body is a closed enum, so the enum widens (§3.4, OQ-4) | As #4. |
| 7 | `POST /api/scheduling-links` `title` | "Trimmed. Required in effect - an empty title is a 422" | any non-blank string | `422 {"error":"title must be 200 characters or fewer"}` | Possible, unverified. **This is the case §3.3 exists for**: without it, a link with a long stored title could not be paused, renamed, re-timed or have a co-host added, because the merged record is re-validated (`handlers_scheduling_links.go:878`). With it, every one of those still works. |
| 8 | `PATCH /api/scheduling-links/{id}` `title` | `nullable: true`, no constraint | same | `422`, same message, **only when the request carries `title`** | As #7. |
| 9 | `POST /api/scheduling-links` `slug` | "Normalised to lowercase alphanumerics and dashes" | any string; normalises without truncation. Over ~2700 bytes: **500 today** | `422 {"error":"slug must be 80 characters or fewer"}`, counted after normalisation | Possible between 80 and ~2700, unverified. A stored slug is a public URL and is editable: a long one can still be replaced by a short one, and the link is otherwise unaffected. |
| 10 | `PATCH /api/scheduling-links/{id}` `slug` | `nullable: true` | same | `422`, same message, only when carried | As #9. |
| 11 | create/PATCH `co_host_emails[]` | `minLength: 1`, `pattern: \S` | any entry `net/mail` parses. Over ~2700 bytes: **500** | `422 {"error":"co_host_emails entries must be 254 bytes or fewer"}` — **bytes** (D-7) | As #4. A recorded invitation's address is never re-sent as an edit; it is kept or dropped by the list. |
| 12 | `POST /api/book/{slug}` `name` | `minLength: 1`, "Trimmed; must be non-empty after trimming" | any non-blank string, up to ~1 MiB, **unauthenticated** | `400 {"error":"name must be 200 characters or fewer"}` | Possible, unverified. A booking is never edited through this endpoint, so nothing becomes un-editable; an over-long stored name stays readable. |
| 13 | `POST /api/book/{slug}` `email` | `minLength: 1`, "Never validated as an email address" | any non-blank string — not even an address (SF-005) | **Superseded: this row is not this spec's change.** The mail-injection work has shipped both halves — an "exactly one address" shape check and `maxAddressLen = 254` **bytes** — each `400 {"error":"email must be a valid email address"}` (§2.8, D-9 revised). This spec **adopts that message and that unit** and adds nothing: `"not an address"` is now refused, and so is a 255-octet address, by one check with one body. What this spec contributes here is only the unit precedent rows 4, 6 and 11 follow. | As #12 — and the risk is now *measurable rather than hypothetical*, because an over-long or malformed address stored **before** their deploy stays stored. §7.1 counts it in octets. |
| 14 | `POST /api/book/{slug}` `notes` | "Free text; no length limit enforced" | any string, up to ~1 MiB, unauthenticated | `400 {"error":"notes must be 2000 characters or fewer"}` | Possible, unverified. As #12. |
| 15 | `POST /api/personal-calendars` `provider` | "Any non-empty string is accepted; there is no allow-list" | any non-empty string | `400 {"error":"provider must be 40 characters or fewer"}`. **No allow-list is added** (§5) | Possible, unverified; `provider` is not patchable (`PersonalCalendarPatchRequest`), so nothing becomes un-editable. |
| 16 | `POST /api/personal-calendars` `name` | "Defaults to the literal \"Personal\" when empty or absent" | any string | `400 {"error":"name must be 200 characters or fewer"}` | Possible, unverified; the label is patchable to a shorter value. |
| 17 | `POST /api/personal-calendars` `url` | "Optional; stored as \"\" when absent" | any string | `400 {"error":"url must be 2048 characters or fewer"}`. Scheme and host stay the webcal session's (§2.8) | Possible, unverified; patchable to a shorter value. |
| 18 | `PATCH /api/personal-calendars/{id}` `name`, `url` | `nullable: true` | same | `400`, same messages, only when carried | As #16, #17. |
| 19 | `POST /api/manager/team/members` `display_name` | *(no request schema in the bundle for this operation; see OQ-5)* | any string, trimmed | `422 {"error":"display_name must be 200 characters or fewer"}` | Possible, unverified; patchable to a shorter value. |
| 20 | `PATCH /api/manager/team/members/{email}` `display_name` | as #19 | any string, no trim | `422`, same message, only when carried | As #19. |

**What is *not* on this list, and so keeps being accepted:** any value within its limit; every field of every operation not named above; every response; every request to `patchEvent`, `parseNaturalLanguage` or `getCalendarFreeBusy` (§5); and — for the three fields the other two sessions own — anything their rules allow (§2.8).

## 4. Acceptance criteria

Layers: `[contract]` a Go test against the HTTP handler and the bundle's shapes; `[unit]` a Go test below HTTP; `[integration]` against a real PostgreSQL 16 (`backend/internal/testdb`, testcontainers); `[e2e]` Playwright on the compose stack; `[doc]` a document assertion.

**Tests before code.** The suite for this work is a new `backend/api/pac164_test.go` plus one case in `backend/api/pac21_4xx_bodies_test.go` for each new refusal body, and a bundle assertion in `backend/api/handlers_contract_extra_test.go`. Stage 3 names the file per criterion; no criterion below may be proven by a test that exists today.

**Fixtures.** `L(n)` is a string of `n` ASCII letters. `over(f)` is `L(limit(f) + 1)`; `at(f)` is `L(limit(f))`. `pad(f)` is `at(f)` with one leading and one trailing space. **O** owns link **K** and team **T**; **P** is a public link with an accepted host.

**The limit is enforced, exactly, at the boundary**

AC-1. For each of the twenty rows of §3.8, given the stated fixture, when the request carries `at(field)` for that field and is otherwise valid, then the operation answers exactly as it does today (201/200) and the stored value is `at(field)`. Today all twenty pass. [contract]

AC-2. For each of the twenty rows of §3.8, when the request carries `over(field)` and is otherwise valid, then the answer is exactly the status and body that row names, **nothing is written or changed** (verified by re-reading the record, or by its absence), and no email, calendar event or ICS is produced. Today all twenty are a success answer, so all twenty fail. [contract]

AC-3. **For every non-address field**, the count is code points, not bytes: given a value of `limit(f)` characters each outside the Basic Latin block (for example U+00E9), every such row of §3.8 answers as in AC-1; given `limit(f) + 1` of them, as in AC-2. A byte-based check would pass AC-1 and AC-2 and fail this. [contract]

AC-3a. **For every address field (§3.8 #4, #6, #11)**, the count is bytes, not code points (D-7): an address of 254 ASCII octets is accepted; the same address with one ASCII character replaced by U+00E9, making 255 octets and still 254 code points, is **refused** with that row's 422. A code-point check accepts the second and fails this criterion. The two criteria together are what make the unit a tested property rather than a comment. [contract]

AC-3b. The address unit matches the one the mail-injection work shipped on `POST /api/book/{slug}` `email` (§2.8): a test sends the 255-octet / 254-code-point address of AC-3a to that operation and to each of §3.8 #4, #6 and #11, and asserts all four refuse. If that work's check is ever changed to code points, this criterion fails and names the divergence rather than letting Paceday carry two address limits. [contract]

AC-4. The count is taken after the trim the field already applies: given `pad(f)` for each field whose §2.4 normalisation trims (team name, link title, booker name, manager `display_name` on add), the answer is as in AC-1 and the stored value is `at(f)` with no surrounding space. Given a field with no trim today (`inviteTeamMember.name`, `addTeamPerson.name`, `notes`, personal-calendar `url`), `pad(f)` is **refused** as in AC-2, because its length is `limit(f) + 2` — this spec adds no trim anywhere (§6). [contract]

AC-5. The slug limit is counted after normalisation: given `"A B "` repeated so that the input is 400 characters and normalises to 79, the create succeeds with that slug; given input normalising to 81, it is the 422. Today both succeed. [contract]

**A carried value only (§3.3) — the compatibility core**

AC-6. Given link K stored with a title of 5000 characters (written directly to the database, as a pre-limit row), when O sends each of `{"active": false}`, `{"buffer_before": 5}`, `{"min_notice_minutes": 10}`, `{"co_host_emails": ["a@x.example"]}` and `{"slug": "fresh-slug"}`, then every one answers 200 and applies, and the stored title is unchanged. When O sends `{"title": "Short"}`, it answers 200 and the title becomes `Short`. When O sends `{"title": L(5000)}`, it is the 422 and nothing changes. A naive check inside `validateSchedulingLink` fails the first five. [integration]

AC-7. Given that same link, `GET /api/scheduling-links/{id}`, `GET /api/scheduling-links` and `GET /api/book/{slug}` each answer exactly as they do today, with the full 5000-character title. [integration]

AC-8. Given a team stored with a 5000-character name, `GET /api/teams/{id}` and `GET /api/teams` answer today's 200 with the full name, and `PATCH` with a short name succeeds. [integration]

AC-9. Given a booking stored with 100000-character notes, the owner's bookings list and the archive read answer today's 200 with the full notes, and the booking can still be cancelled. [integration]

AC-10. Given a personal calendar stored with a 5000-character label, `GET /api/personal-calendars` answers today's 200, and a PATCH carrying only `{"enabled": true}` succeeds with the label unchanged. [integration]

AC-11. **Structural.** A test reads every handler changed by this work and asserts that each length check is reached only on a path where the request carried the field: for every `(operation, field)` pair of §3.8 with a PATCH form, a request body that omits the field produces **zero** calls to the length check (a counting seam). Today there is no check to count; a merged-record implementation records a call and fails. [unit]

**Indexed columns: a refusal, not a 500**

AC-12. Given a `createSchedulingLink` whose `slug` normalises to 3000 characters, the answer is the §3.8 #9 422 and no link exists. Today it is a 500 from SQLSTATE `54000` (the index entry exceeds the btree maximum) and no link exists either, so the status and body change and the write outcome does not. [integration]

AC-13. Given `inviteTeamMember`, `addTeamPerson` and a `co_host_emails` entry each carrying a 3000-character address that `net/mail` parses, each answers its §3.8 422 and writes nothing. Today each is that operation's 500. [integration]

**Nothing reaches a sink over the limit**

AC-14. Given public link P whose stored title is `at(title)` and a booking whose `name` is `at(name)` and `email` is `at(email)`, when the booking is saved, then the captured SMTP payload's `Subject:` line, its `To:` header, and the ICS `SUMMARY:`, `ATTENDEE;CN=` and `mailto:` values each carry the full value and the ICS parses as one `VEVENT`. (It is the *bound*, not the folding, that this criterion fixes; folding and CR/LF belong to the mail-injection work, §2.8, and this criterion must not assert their absence.) [unit]

AC-15. Given the same link with a 5000-character **stored** title (a pre-limit row) and a valid booking, the booking is still saved and the email is still attempted — an over-long stored value never turns a booking into a refusal or a 500. [integration]

**The declared limit and the enforced limit are one number**

AC-16. A test asserts, for each of the twenty rows of §3.8, that the `maxLength` in `contracts/openapi/openapi.yaml` on that request property equals the number the handler enforces, read from one exported table rather than restated. Today neither side exists for nineteen of them. [contract]

AC-17. No response schema in the bundle gains a `maxLength` (§3.5): the set of `maxLength` keywords under response schemas is identical before and after, and the three new request-side `maxLength`s on `HabitUpdateRequest`-style shared schemas (if the contract stage shares any) are shown not to be reachable from a response. [contract]

AC-18. `make openapi-check` passes, and `contracts/openapi/MIGRATION.md`'s `generated` count is **unchanged at 14** — this work moves no row (§0). [contract]

**The habits precedent is repaired (§2.7)**

AC-19. Given a habit stored with a 5000-character title (a row of the 2026-04-26 to 2026-08-18 window), when its owner sends `{"active": false}`, then it answers 200 and applies. Today it answers `400 {"error":"title must be 200 characters or fewer"}` for a value the request did not send, so this fails. A PATCH carrying a 5000-character title stays the 400. [integration]

**Documentation and operations**

AC-20. The pre-deploy query of §7.1 runs against a database seeded with one row over each proposed limit and reports exactly those rows, one line per table and column with the maximum length found. [integration]

AC-21. Every new refusal is declared on its own operation in the bundle, with its exact body, and each operation in scope also declares the body-cap 400 (API-097, inherited scope). A test asserts that each of the twenty bodies of §3.8 appears as a documented response of its operation. [contract]

AC-22. `docs/factory/api-audit.md` and `docs/factory/silent-failures.md` carry the re-marks this spec's Inherited scope promises (SF-005 **Fixed (mail-injection work)**, SF-043 partly addressed, API-075 passed over with its reason, API-097 narrowed), each naming this work. [doc]

AC-23. The row of `docs/security.md` that today names the 1 MiB cap ("nginx caps bodies at 1 MiB and upstream waits at 130 s", the B1 row) also names the field limits, so the two bounds are documented in one place. [doc]

**The web still works**

AC-24. The link editor creates and renames a link, the public page books a slot, and the team dialog adds a person, all with ordinary-length values, unchanged. [e2e]

AC-25. A form submitted with a value over the limit shows the server's message rather than failing silently. **Whether the web needs its own `maxlength` attributes to make that pleasant is OQ-3**; this criterion asserts only that the refusal is surfaced, not where it is enforced. [e2e]

## 5. Explicitly out of scope

- **Migrating any operation to the generated strict wrapper.** It would not enforce a single constraint (§0, §2.1), it is one-way and gated, and bundling it here would make this diff unreviewable. `MIGRATION.md`'s `generated` count must not move (AC-18).
- **A request-validation middleware** (kin-openapi `openapi3filter` or similar) that would enforce the bundle's keywords generically. It is the genuinely interesting alternative to D-1, and it is a large architectural change with its own failure modes: every existing operation's 4xx bodies would come from the validator rather than the handler, which contradicts the deliberate per-operation error bodies PAC-21 established. Recorded as OQ-6, not decided here.
- **Fields that are not stored**: `patchEvent`'s `title`, `description`, `location` and `attendees[]`; `parseNaturalLanguage`'s `text`; `getCalendarFreeBusy`'s `emails[]`. Each has a real cost (the first goes to Google, the second to a metered language model, the third to a provider), but each is a different kind of decision — an outbound-provider limit, not a storage limit — and each needs its own number from a different source. They are a named follow-up, **FU-1**, with the `text` field the most urgent of the three because the body cap is today its prompt-size cap.
- **Settings, SSO provider and organisation-AI free text** (`focus_label`, `webcal_url`, `llm_base_url`, `azure_endpoint`, `aws_region`, model names, API keys, `provider_name`, client secrets, SAML metadata). All unbounded, all stored. They are excluded on purpose: PAC-66 decided *deliberately* not to constrain several of them so that pre-PAC-66 rows stay valid (`openapi.yaml:3777`, `:3786`, `:3837`), and re-opening that needs that spec's reasoning in front of the reader. Follow-up **FU-2**.
- **Habit `color`** (unbounded) and the `HabitTemplate` clock patterns, where the contract is *looser* than the server (§2.3). Follow-up **FU-3**.
- **Any rule of this spec's own on `POST /api/book/{slug}` `email`.** Both halves of SF-005 are shipped by the mail-injection work, in bytes, with a message (§2.8, D-9); this spec adopts them (§3.8 #13). **URL content validation** on the personal-calendar `url` (SF-043's other half, D-10) belongs to the webcal session of §2.8.
- **The render-boundary guard itself** (`safetext.MaxLen`, §2.8(c)). This spec judges whether it is consistent with D-2 and records the answer (D-15), but it does not specify its value, its unit, or whether it truncates or fails the send — that is the mail-injection work's to state.
- **A `provider` allow-list** for personal calendars (SF-043's first half). Length is bounded; which providers are legal is a product decision with a sync path behind it.
- **API-075** (`enabled` is not a pointer). Named in Inherited scope and deliberately passed over: it changes what an existing request *means*, which is a different class of change from refusing one.
- **`inviterName`, `teamName` in the invite email, and host names in the ICS.** They come from `users.name` and `teams.name` at read time, not from a request body (§0). Bounding `teams.name` (#1, #2) bounds the second of them as a side effect; the first is bounded only by what the identity provider returns, and no request validation can reach it.
- **Column constraints or a backfill.** No migration is added (§7).
- **Truncating or rewriting any stored value**, now or later. §3.3 makes it unnecessary.
- **Rate limiting.** Unchanged; `createPublicBooking` keeps its 20-per-10-minutes (NEW-booking-rate-limit).

## 6. What must not change

- **The body cap and its answers.** 1 MiB, the 413, and the read-failure 400 (`middleware.go:411-440`). `backend/api/pac81_middleware_test.go` and `pac120_test.go`'s cap assertions must pass untouched.
- **Every existing success answer for an ordinary-length value**, on all 134 operations. The suites that pin them: `handlers_teams_test.go`, `handlers_scheduling_links_test.go`, `handlers_booking_test.go`, `handlers_personal_test.go`, `handlers_manager_test.go`, `pac112_test.go`, `pac119_test.go`, `pac120_test.go`, `pac129_test.go`, `pac132_test.go`, `pac149_test.go`, `pac150_test.go`, and e2e `scheduling-links.spec.ts`, `public-booking.spec.ts`, `pac129-team-people.spec.ts`, `pac112-cohosts.spec.ts`, `manager-team.spec.ts`.
- **Every existing refusal's status and body**, including the order in which refusals are evaluated. The new check is added where it does not move an existing one: `pac21_4xx_bodies_test.go`, `pac21_error_bodies_test.go` and `pac25_contract_test.go` must pass unchanged, and the documented evaluation orders of `createSchedulingLink` (key 400 → body 400 → field 422s) and `createPublicBooking` (429 → 404 → 410 → 400 → 409) are unchanged. The new 400s and 422s join their operation's existing field-refusal step.
- **The habit title's 200-rune limit, its 400 and its message.** `handlers_habits_validation_test.go`. AC-19 changes only *when* it is applied, never the limit or the text.
- **The idempotency-key and time-zone limits.** `pac82_test.go`, `pac119_test.go`, `pac88_listing_test.go`.
- **No new trim, lowercasing or normalisation anywhere.** A field that is not trimmed today is still not trimmed (AC-4 pins this as a refusal, which is the honest consequence). Adding a trim would change a stored value, which no criterion asks for.
- **The address normaliser's rule.** `storage.ValidAddress` (`backend/storage/address_trim.go:47-55`) is unchanged; the length check sits beside it, never inside it, so `paceday-operator erase-address` (PAC-134) and the sign-in paths that share it are untouched. Its only tests today are `backend/storage/pac129_test.go` and `pac134_test.go` — there is no `address_trim_test.go`, which is worth recording — and both must pass unchanged.
- **Every response shape and every generated response type** (§3.5, AC-17).
- **`MIGRATION.md`'s generated count** (AC-18).
- **The two in-flight works' fields**, as §2.8 draws the line. This work must merge cleanly beside either order.

## 7. Rollback

Revert the stage-4 PR and the contract PR. Every field is unbounded again, exactly as at `c3d23aa`. **There is no migration and no stored data is changed by this work**, so rollback loses nothing and needs no restore: a value accepted while the limits were live is within them, and a value stored before them was never touched. The web regenerates its types from the reverted bundle.

The one asymmetry: a client that learned to shorten its input keeps working after a rollback. Nothing in the other direction breaks.

### 7.1 Pre-deploy gate (release manager, before the stage-4 PR is deployed)

This is the **primary** gate, and it is why §3.6 recommends no log-only phase. It is run on production before release and reported to the owner.

```sql
-- Longest stored value per bounded field, and how many exceed the proposed limit.
-- :limits are §3.1's numbers; edit the second column if the owner changes one (OQ-1).
-- Address columns use octet_length, every other column char_length (D-7, revision 1):
-- an address limit is an octet limit, so counting characters there would under-report.
WITH f(label, lim, longest, over) AS (
  SELECT 'teams.name',                              200, max(char_length(name)),          count(*) FILTER (WHERE char_length(name) > 200)                  FROM teams
  UNION ALL SELECT 'scheduling_links.title',        200, max(char_length(title)),         count(*) FILTER (WHERE char_length(title) > 200)                 FROM scheduling_links
  UNION ALL SELECT 'scheduling_links.slug',          80, max(char_length(slug)),          count(*) FILTER (WHERE char_length(slug) > 80)                   FROM scheduling_links
  UNION ALL SELECT 'bookings.booker_name',          200, max(char_length(booker_name)),   count(*) FILTER (WHERE char_length(booker_name) > 200)           FROM bookings
  UNION ALL SELECT 'bookings.booker_email (octets)',254, max(octet_length(booker_email)), count(*) FILTER (WHERE octet_length(booker_email) > 254)         FROM bookings
  UNION ALL SELECT 'bookings.notes',               2000, max(char_length(notes)),         count(*) FILTER (WHERE char_length(notes) > 2000)                FROM bookings
  UNION ALL SELECT 'personal_calendars.name',       200, max(char_length(name)),          count(*) FILTER (WHERE char_length(name) > 200)                  FROM personal_calendars
  UNION ALL SELECT 'personal_calendars.url',       2048, max(char_length(url)),           count(*) FILTER (WHERE char_length(url) > 2048)                  FROM personal_calendars
  UNION ALL SELECT 'personal_calendars.provider',    40, max(char_length(provider)),      count(*) FILTER (WHERE char_length(provider) > 40)               FROM personal_calendars
  UNION ALL SELECT 'team_added_members.display_name',200,max(char_length(display_name)),  count(*) FILTER (WHERE char_length(display_name) > 200)          FROM team_added_members
  UNION ALL SELECT 'team_added_members.email (octets)',254,max(octet_length(email)),      count(*) FILTER (WHERE octet_length(email) > 254)                FROM team_added_members
  UNION ALL SELECT 'team_invites.invitee_email (octets)',254,max(octet_length(invitee_email)),count(*) FILTER (WHERE octet_length(invitee_email) > 254)    FROM team_invites
  UNION ALL SELECT 'manager_team_members.display_name',200,max(char_length(display_name)),count(*) FILTER (WHERE char_length(display_name) > 200)          FROM manager_team_members
  UNION ALL SELECT 'habits.title',                  200, max(char_length(title)),         count(*) FILTER (WHERE char_length(title) > 200)                 FROM habits
)
SELECT label, lim, coalesce(longest, 0) AS longest, over FROM f WHERE over > 0 ORDER BY over DESC, label;
```

Add one more branch for `scheduling_link_hosts.invitee_email` (migration `044_cohost_address_invitations.up.sql:9`, limit 254 **octets**, `WHERE invitee_email IS NOT NULL`). It carries a functional index on `lower(invitee_email)` (`044:102`), so it has the same btree ceiling as the other indexed address columns.

- **Empty result**: no stored row violates any proposed limit. Release proceeds, and §3.8's "stored rows at risk" column becomes "none" rather than "unverified".
- **Any row**: the release **does not proceed on the spec's numbers.** The release manager reports the table to the owner, who either raises that limit above the observed maximum (OQ-1) or accepts the rows staying as they are — which §3.3 already makes safe, but which is the owner's call to make knowingly, not the factory's to assume (OQ-2).
- `habits.title` is in the query on purpose: it measures §2.7's existing breakage, which AC-19 repairs. A non-zero count there is expected and is not a stop condition.

### 7.2 Release note

- **What changes for a person using Paceday**: nothing, unless they were typing a very long value into a short field. Then they get a message saying the maximum and are asked to shorten it.
- **What changes for an API client**: nine operations gain a refusal. §3.8 is the complete list, with each status code and body.
- **Existing long values are kept.** They are readable everywhere they were readable before, and every record carrying one can still be edited — including the field itself, by sending something shorter.
- **Two answers improve.** An over-long address or slug used to be a 500; it is now a refusal that says what is wrong.
- **The booker address is already fully validated** by the mail-injection release, in shape and in length (254 octets), with the body `email must be a valid email address`. This release adds nothing there; it applies the same 254-octet bound to the three other address fields, so Paceday has **one** address limit rather than two.
- **Address limits are counted in octets, every other limit in characters.** A non-ASCII address can therefore be refused while looking shorter than the number — which is the protocol's rule, not a Paceday choice.
- **Before deploy**, run §7.1 and report it to the owner.

## 8. Open questions

- **OQ-1 (owner, blocking the numbers only; default: §3.1's table).** Are the thirteen limits right? Each is argued from a sink or a precedent, but a limit is a product judgement — "how long may a team name be" is not derivable from the code. §7.1 gives the owner the data to answer with. The design (§3.2–§3.5) does not depend on the numbers; only the table does.
- **OQ-2 (owner, conditional on §7.1; default: raise the limit above the observed maximum).** If production holds rows above a proposed limit, is the limit raised, or are those rows left as they are? §3.3 makes "left as they are" safe, but it means a record whose field can only ever be shortened.
- **OQ-3 (owner / frontend, non-blocking; default: server-side only, and the web shows the message). [unverified-web]** Should the web also enforce the limits in its forms (`maxlength`, a character counter)? That is better for the person typing, and it needs the limits generated from the bundle rather than restated. A reader must open `smart-calendar-flow/src/components/links/LinkEditDrawer.tsx`, the public booking form and the team people dialog to see what those fields do today. In-repo evidence that the web sends short values is indirect but real: `docs/factory/silent-failures.md:189` records that SF-043 is "masked because the web sends `webcal` and `enabled:true`", and every value in `e2e/seed/seed.sql` and every `fill()` in `e2e/tests/*.spec.ts` is under 40 characters. None of that is proof about the production app.
- **OQ-4 (stage 2, contract-author).** `addTeamPerson`'s 422 is a closed four-value enum (`gen/paceday.gen.go:73-78`). Widening it changes the generated Go type and the web's zod schema. Options: widen the enum; put the length refusal on that operation's free-string 400 instead (which conflicts with D-4's reasoning); or give it its own response. This needs the contract stage's judgement, not the spec's.
- **OQ-5 (stage 2).** The manager member add and patch operations' request bodies could not be found as named schemas in the bundle (§3.8 #19). Either they are inlined under another name or the operations are not fully modelled. The contract stage must establish which before declaring a limit there; if the request shape is absent, that absence is itself a finding to record.
- **OQ-6 (owner / architecture, non-blocking; default: no).** Should Paceday mount a generic request-validation middleware so the bundle's keywords are enforced once rather than per handler? It would make §0's premise 2 true going forward and would retire the whole class of defect. It would also take every 4xx body on 134 operations out of the handlers that currently own them, against PAC-21's per-operation bodies. Large enough to be its own spec.
- **OQ-7 (stage 3, plan-author).** Where does the single source of the limits live so that AC-16 can compare the contract and the handler without restating either? The habit title's number is a literal in `validateHabitFields` today; thirteen more literals in nine handlers would drift. The plan decides between one exported table in the api package and generating it from the bundle.
- **OQ-8 (triage, non-blocking).** FU-1 (unbounded `text` to the language model, and the event fields), FU-2 (settings, SSO and organisation-AI free text) and FU-3 (habit `color`, the `HabitTemplate` clock patterns) each need an issue. FU-1's `text` field is the one with a direct billing consequence and should be filed first.

## 9. Decisions

| ID | Decision | Mark |
|---|---|---|
| D-0 | **The commissioning audit's root cause does not hold, and this spec says so rather than building on it.** The generated strict wrapper enforces no constraint (`gen/paceday.gen.go:24764`); the contract declares no limit on any field named in the brief and says so in prose; `AddTeamPerson` is a counter-example, not a model; `inviterName` is not a request field. What survives is the harm, not the diagnosis, so the work is "declare and enforce limits that do not exist" — a product decision — not "enforce the contract" (§0) | [F] |
| D-1 | **Limits are declared in the contract and enforced by hand in the handler, as one number proved equal by a test** (AC-16). This is the house pattern — the habit title, the idempotency key and the time zone all work this way — and the only one available, since no generator carries the keyword (§2.1). A generic validation middleware is the alternative and is OQ-6 | [F] |
| D-2 | **Refuse, never truncate.** Truncation cannot be reported in this API's answer shapes, can turn one address or URL into a different valid one, and produces mojibake that is then stored for ever. Refusal is the only outcome the client can act on (§3.2) | [F] |
| D-3 | **A limit is checked only against a value the request carries**, a deliberate narrow exception to the merged-record rule, following the existing `slugPatched` shape. Without it, a stored over-long value makes its record un-editable through its own endpoint — which has already happened once in this codebase, with habit titles, unnoticed since 2026-08-18 (§2.7, AC-6, AC-19) | [F] |
| D-4 | **Each operation refuses in its own existing status-code family**, with the message form `"<field> must be <N> characters or fewer"`. A uniform code across nine operations would change each one's answer family — a larger break than the limits — and would break clients that branch on the code (§3.4) | [F] |
| D-5 | **No `maxLength` on any response schema.** Rows written before the limit are kept and returned, so a response constraint would make the contract false for them and would make the web's zod schema reject a record the server holds. This is the bundle's own existing reasoning for `awsRegion` and `llmBaseUrl`, applied unchanged (§3.5) | [F] |
| D-6 | **No log-only phase. Measure the stored rows with one query, choose limits above what is there, enforce in one release.** Log-only observes writes in a window; the risk is stored rows, which it cannot see. §7.1 answers the real question completely in one pass, and D-3 already removes the failure log-only would guard against (§3.6) | [F] |
| D-7 | **Lengths are counted in code points — except addresses, which are counted in bytes** (revision 1). Code points for human-facing labels, because that is what the one existing limit counts (`len([]rune(...))`) and what a person means by "characters"; after the existing trim so the limit composes with normalisation rather than competing with it; after slug normalisation because that is the value that is stored, indexed and put in a URL. **Addresses are the deliberate exception, and the reason is that an address limit is not about reading.** 254 is RFC 5321's maximum forward path *in octets*: it exists because the SMTP envelope has that many bytes for it, and the question "will this address fit the protocol field we are about to put it in" has only a byte answer. A code-point limit would be wrong in both directions — it would accept a 254-code-point non-ASCII address of up to 1016 octets that no SMTP exchange can carry, and it would be indistinguishable from the byte limit for the ASCII addresses that are almost all of them, so the error would only ever surface on the users least able to diagnose it. It would also split Paceday's address limit in two, because the mail-injection work's shipped `maxAddressLen = 254` is bytes (§2.8); one field, one number, two units is a defect waiting to be found. Pinned as a tested property, not a comment, by AC-3, AC-3a and AC-3b | [F] revised 2026-11-22 |
| D-8 | **No trim, lowercasing or normalisation is added anywhere.** Four fields have none today and keep none, which means a padded value at the limit is refused (AC-4). Adding a trim would silently change a stored value, which is D-2's objection in another costume | [F] |
| D-9 | **Superseded by what shipped: SF-005 is fully closed by the mail-injection work, and this spec adopts its rule rather than adding one.** The draft deferred the booker address's *shape* to that session and kept its *length* here. That session has now shipped both — an "exactly one address" check and `maxAddressLen = 254` bytes — under one body, `400 {"error":"email must be a valid email address"}`. So the correct resolution is not "partly addressed" but **"Fixed (mail-injection work)"**, and this spec declares nothing on that field: it adopts their message and their unit, and cites them (§3.8 #13, §5). Deferring a half and then adding a second rule to the same field once the other half landed would have been the worst of both — two messages, two numbers, one field. What this spec keeps from the episode is the *unit*, which it propagates to the three address fields that session does not touch (D-7) | [F] revised 2026-11-22 |
| D-10 | **SF-043 is split: `provider` length is in scope, the URL's content is not.** The URL's scheme and host belong to the webcal session (§2.8). `url` still gains a length bound, because length and content are independent checks and neither implies the other. The `provider` allow-list is out of scope (§5) | [F] |
| D-11 | **No operation is migrated to the generated strict wrapper, and `MIGRATION.md`'s generated count stays at 14** (AC-18). The migration buys this work nothing (D-0) and would make the diff unreviewable (§5) | [F] |
| D-12 | **No migration, no backfill, no column constraint.** Rollback therefore loses nothing (§7). A `CHECK` constraint would be a second enforcement point that could disagree with the handler, and adding one on a live table needs the stop-migrate-start procedure (PAC-116) for no gain D-3 does not already give | [F] |
| D-13 | **Turning the indexed-column 500s into refusals is in scope, and is a change of answer, not only of code path.** An over-long address or slug is `54000` today, which `isUniqueViolationOn` does not match (`storage/scheduling_links.go:940-943`), so it surfaces as the operation's 500. After this work it is the field's refusal. Both leave nothing written; only the answer changes (AC-12, AC-13) | [F] |
| D-14 | **The habits precedent is repaired as inherited scope** (AC-19), because this work's whole compatibility argument is the rule that case violates, and shipping D-3 on nine operations while leaving the tenth broken would be incoherent | [F] |
| D-15 | **A length cap at the rendering boundary is consistent with D-2, not an exception to it — with one carve-out that is a defect in the shipped form.** D-2 ("refuse, never truncate") is an argument about the **entry** boundary, and four of its five reasons are reasons *to a client*: a truncation cannot be reported in an answer, the house refuses with a message, and the person can resend something shorter. At the rendering boundary none of that holds. By the time the mail and ICS composers run, the booking row is already committed (`engine/booking.go`: save, then confirmation) and the HTTP answer is already sent; there is no response left to carry a refusal and no caller left to act on one. So the governing rule there is a different one, and this spec states it: **never emit a value that makes the message unparseable, never let the sink silently change a value's meaning, and never alter what is stored.** A 200-rune cap on a human-readable label satisfies all three — the label is cosmetic in that context, the stored value is untouched (which also keeps D-3 and D-12 intact), and the alternative, failing the send, punishes the *booker* with no confirmation for a title they did not write. **The carve-out: `safetext.MaxLen` must not be applied to an address.** D-2's second reason is the one that does survive into the render boundary, because truncation there changes *meaning*: a cut address is another person's valid address, and a cut `mailto:` is a different recipient. Their two numbers collide on exactly this — an ASCII address of 201 to 254 octets passes their 254-byte entry gate and is then cut to 200 runes by the composer, so Paceday would send a confirmation, and an ICS `ATTENDEE`, to an address nobody asked for. **This spec's position:** the render guard is right and should ship, applied to labels only (link title, booker and host names, team name, inviter name); addresses must be excluded from it and bounded at entry instead, which that work already does. If an address somehow reaches a composer over the limit, the send fails and is reported — a missing confirmation is recoverable, a confirmation sent to a stranger is not. Whether the guard truncates or fails for labels is that work's call to state; either satisfies the rule above. Once this spec's entry limits ship, the guard becomes reachable only for rows stored before them — entry gate for new data, render guard for old data, which is the right layering and is why the two 200s are the same number at two boundaries rather than a duplication | [F] 2026-11-22 |
