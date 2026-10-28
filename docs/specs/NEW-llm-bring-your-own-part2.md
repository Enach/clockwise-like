# Spec — NEW-llm-bring-your-own, Part 2: an organisation-wide AI provider and key

- **Linear**: PAC-73 (https://linear.app/paceday/issue/PAC-73). Part 2 of the
  spec `docs/specs/NEW-llm-bring-your-own.md` (the "parent spec").
- **Status**: challenged; revision 2 (2026-10-28) answers challenge
  revision 1. The text challenged at `a90ba5e` called itself "revision 1";
  the challenger recorded it as revision 0, and this file counts that way:
  revision 0 is the text challenged at `a90ba5e`, revision 1 the text
  challenged at `0c81d42`, revision 2 this one. Each challenge is recorded
  verbatim at the end of the file, followed by the revision table answering
  every finding ("Revision 1", "Revision 2"). Sections 2 to 9 are the live
  text.
- **Author**: spec-author
- **Supersedes**: the parent spec's "Part 2" acceptance criteria (AC-22 to
  AC-31, AC-36, AC-38, AC-41 and the Part 2 rows of AC-40) and the Part 2
  clauses of its §3. They were written before the owner answered OQ-1, OQ-7
  and OQ-8 (2026-10-28) and before PAC-50 landed. The mapping is in §4.0. Part
  1's criteria and behaviour are unchanged except where §3 here says so.
- **Inherited scope**:
  - No `x-uncertain` marker sits on an operation this part changes (settings
    read, natural-language parse, brief refresh, the provider probe, the SSO
    admin step-up start, its options read and its callback). Checked with
    `grep -n x-uncertain contracts/openapi/paths/*.yaml` on `ae5c2cf`: the
    markers are in `calendar.yaml` (1309, 1771), `habits.yaml:475` (the
    meeting-brief **read**, not changed) and `teams.yaml` (610, 1640, 1647).
  - No open finding in `docs/factory/api-audit.md` is on these operations.
    The rows that name them (API-002, -003, -004, -013, -025, -037, -038,
    -080, -081, -082, -093) are Fixed; API-039 is Claimed with its
    `LLMProvider` half Fixed by Part 1 (`api-audit.md:297`).
  - **PAC-50 AC-27** (`docs/specs/PAC-50.md:268`): a grant of another purpose
    is never consumed by an SSO write, and the reverse. This part is the
    "later feature" that test stands in for; it must keep AC-27 true.

## Why a separate file

The parent spec is 1,852 lines, most of them three challenge rounds and two
revisions about Part 1, which has shipped (`8c542de`). Part 2 needs a new
state table (the owner's O-12 adds three rows), new criteria for O-12 and
O-13, a migration and its own challenge record. Appending that as "revision 3"
would make a challenger of Part 2 read Part 1's history to find the live
text, and would mix a shipped part's record with a draft. The parent spec
keeps its decisions (O-1 to O-13, D-2 to D-21), which this file cites rather
than copies; a line in the parent's §0 points here.

## 1. Problem

The owner decided that Paceday does not run or pay for AI, and that an
organisation admin can set one provider and key that the organisation's
members use (parent O-1, O-5). Part 1 removed every deployment credential and
let each user bring a key. It deliberately left organisations out: today a
member who wants AI must obtain and paste a key of their own, so an
organisation that wants its staff to have AI must hand the same key to every
member, spreading the secret across as many accounts as there are people, and
cannot take it back from one place. Since Part 1, every new account is stored
as "follow my organisation" and answers "no AI provider is set up", with no way
for anyone to change that except the user pasting a key.

The owner also decided (2026-10-28):

- only a proven organisation administrator may set or change the
  organisation's provider and key, proven again for every change (O-11);
- an administrator may forbid personal keys, so every member uses the
  organisation's account (O-12);
- before a member's first AI request on the organisation's account, the member
  is told that their text and meeting details go to that account, whose
  administrators can **access** them outside Paceday, and is asked once;
  nothing is sent before they confirm (O-13; the owner's words were "tell and
  ask once before the first AI request").

## 2. Current behaviour (main at `ae5c2cf`)

### 2.1 How a request's AI configuration is decided

- One function decides every AI request and the settings read:
  `decide` in `backend/nlp/llm_factory.go:106-129`, called through
  `DecideSaved` (`:47-49`, parse at `backend/nlp/parser.go:169`, brief refresh
  at `backend/api/handlers_meeting_briefs.go:88`), `Status` (`:52-58`, the
  settings read at `backend/api/handlers_settings.go:79`) and `DecideProbe`
  (`:78-100`, the probe at `backend/api/handlers_llm.go:75`).
- A caller stored as "follow my organisation" always gets "nothing
  configured": `llm_factory.go:110-112` ("Part 1: no organisation configuration
  exists (row 6)").
- Every not-configured answer names only the user as able to fix it:
  `notConfigured` sets `Enablers: []string{EnablerUser}` (`:161-166`); the
  `organization_admin` constant exists (`:32`) and is never used.
- Organisation membership is read before deciding (`handlers_llm.go:56`,
  `parser.go:162`, `handlers_meeting_briefs.go:83`, via
  `backend/storage/settings_ai.go:142-152`) and changes nothing (`decide`
  ignores `inOrg`, comment at `:102-105`).
- A configured request reads every destination value from the caller's own
  settings (`newProviderClient`, `:133-154`).
- New accounts are stored as "follow my organisation":
  `backend/storage/migrations/029_llm_bring_your_own.up.sql:14`.

### 2.2 Organisations and the administrator proof (PAC-50, landed)

- Membership is by email domain and self-service
  (`backend/storage/orgs.go:42-52`). There is no stored admin role (parent
  §2.3).
- PAC-50's grant table already carries a purpose:
  `030_sso_admin_stepup.up.sql:13-33` (`purpose TEXT NOT NULL CHECK (purpose
  <> '')`, no default, no list of values, "a later feature reusing the admin
  proof (the organisation AI key, owner 2026-10-28) adds its own value without
  a migration"). The consume matches the purpose
  (`backend/storage/sso_stepup.go:122-133`); the SSO write consumes only
  `sso_write` (`backend/api/handlers_sso_stepup.go:88-93`).
- But every grant is **issued** as `sso_write`: the callback's record step
  calls `issueGrant(..., SSOGrantPurposeSSOWrite, ...)`
  (`sso_stepup.go:208`); the signed state cookie binds user, org, provider,
  nonce and expiry and no purpose (`handlers_sso_stepup.go:327-333`); the start
  takes only `provider` (`:392-441`). So there is no way today to obtain a
  grant of any other purpose. PAC-50 D-15(5) leaves "how a step-up for another
  purpose is started" to this ticket (`PAC-50.md:345`).
- The write gate (`authorizeWrite`, `handlers_sso_stepup.go:228-252`):
  consume a live grant, else a break-glass address
  (`PACEDAY_ADMIN_EMAILS`) whose session was issued under 10 minutes ago
  (`:238-246`), else 403 `sso_write_requires_admin_stepup` with the step-up
  URL, the scopes each configured provider would request and what is lost
  without it (`:254-280`).
- The callback always lands on `<FRONTEND_URL>/settings?stepup=granted` or
  `...?stepup=denied&reason=<r>` (`:563-566`). The web's SSO step-up section
  reads exactly those parameters
  (`smart-calendar-flow/src/components/settings/SsoAdminStepupSection.tsx:40-57`).
- Operator records: `sso_events` accepts kinds `sso_stepup` and
  `sso_provider_written` only (`030_sso_admin_stepup.up.sql:43`).

### 2.3 What clients show today

- The web shows a stored "follow my organisation" choice, to everyone, as a
  disabled "No provider chosen" (`smart-calendar-flow/src/lib/aiProviders.ts:55-64`,
  parent D-21), and no organisation option or wording.
- The MCP server's `clockwise_parse_command` passes the parse body through
  (`mcp/tools.go:195-203`) and `clockwise_get_settings` the settings document
  (`mcp/tools.go:233-238`). No MCP tool reaches the probe, brief refresh or
  any organisation operation.

### 2.4 Tests that pin the Part 1 behaviour this part changes

`TestDecideSaved_OneStateOneReason` and `TestBYOK_EveryNotConfiguredRowGivesOneReason`
(rows 6 and 8 inside an organisation answer `enablers: [user]`, parent
revision 2) pin the Part 1 answer. They are amended, not deleted: in an
organisation with no configuration, rows 6 and 8 now add the organisation's
admin (§3). PAC-50's `TestPAC50_*` grant tests and AC-27 must keep passing
unchanged.

Other consumers that pin the Part 1 answer for an organisation member, and
are amended (not deleted) when this part lands (revision 1, challenge "Consumers
this breaks"):

- `e2e/tests/llm-bring-your-own.spec.ts:86-121`, the test "AC-21 (Part 1,
  spec revision 2): an organisation member following the organisation is
  sent to no admin": it asserts `enablers: ['user']` for an organisation
  member (96, 100, 103), no "admin" in the message or notice (98, 112), the
  disabled "No provider chosen" (114-115), no organisation option among the
  enabled choices (116-117) and no organisation wording (118). P2-AC-14, P2-AC-31 and P2-AC-35 reverse
  each of these for members.
- Part 1's manifest criterion AC-34
  (`contracts/features/NEW-llm-bring-your-own.yaml:149`): "a fresh
  organisation member … enablers [user] … naming no admin".
- `smart-calendar-flow/src/lib/aiProviders.test.ts:57-66` (on
  `smart-calendar-flow` `origin/main`): a stored `organization` is "No
  provider chosen", disabled, for everyone; P2-AC-31 changes it for members.
- `smart-calendar-flow/src/components/settings/SsoAdminStepupSection.tsx:38-58`:
  shows any `stepup=granted` landing as an SSO outcome ("used by your next
  SSO configuration change"), whatever the purpose; P2-AC-32 changes it.

### 2.5 Provider error text today (where a key can leak)

- A provider's non-success answer is copied verbatim into the error:
  `fmt.Errorf("LLM API error %d: %s", …, string(respBody))`
  (`backend/nlp/parser.go:122-123`).
- A failed brief stores that error as the brief's text,
  `fmt.Sprintf("LLM error: %v", err)`
  (`backend/engine/meeting_brief_service.go:87-90`), which the member reads
  back through the brief read.
- The probe replaces the caller's own stored and submitted keys with
  `[redacted]` before logging (`backend/api/handlers_llm.go:61-70`, used at
  `:122`); the 502 body is the fixed `upstream provider error`.
- A parse failure is logged and reported with the unredacted error
  (`backend/api/handlers_nlp.go:28-31` → `writeServerError` →
  `logServerError`, `backend/api/server_errors.go:41-49`, which writes the log
  line and reports to Sentry).

Under Part 1 the only key such a text could contain was the caller's own.
Under this part it can contain the organisation's key, which members must
never hold, and an OpenAI-compatible gateway (owner O-10) may echo the key in
its error body.

## 3. Desired behaviour

### 3.1 Who decides, and the one table

For each user, at each AI request (natural-language parse, meeting-brief
refresh, the provider probe) and at each settings read, exactly one row
applies. Rows are evaluated **in the order listed** (the numbers are the parent
spec's, kept so its tests keep their names; 9 to 11 are new and come right
after row 1). Only rows 2 and 4 send anything. Row 9 is reserved and is not
reachable while P2-D2's current default stands (revision 1; OQ-P2-2).

"Complete" means the organisation has a provider, a key for it, and every
destination value that provider needs. "Forbids" means an administrator has
forbidden personal keys. "Confirmed" means the user's stored confirmation
names exactly the version of the organisation's configuration that this
request reads (§3.4); a confirmation of any other version, or one ended by
the user's own choice (§3.4), does not count. A version fixes the provider,
the key and the destination values; the model and the personal-keys setting
are not part of it (§3.4, revision 2).

| # | User's stored choice | Condition | Outcome and reason | Who can fix it |
|---|---|---|---|---|
| 1 | Off | none | not configured: off by the user's choice | the user |
| 9 | (reserved) | not reachable under P2-D2's current default. If the owner answers OQ-P2-2 the other way it becomes "own or removed provider; forbids; complete; confirmed → configured: the organisation's" | - | - |
| 10 | Own provider, or a removed provider | organisation forbids; complete | not configured: **personal keys are forbidden by your organisation; your own provider and key are kept but not used; choose "Use my organisation's AI" in Settings** | the user, by choosing "Use my organisation's AI" (which shows the §3.4 notice and confirms) |
| 11 | Own provider, or a removed provider | organisation forbids; not complete | not configured: **personal keys are forbidden by your organisation and it has not finished setting up AI** | the organisation's admin only |
| 2 | Own provider | a key bound to it | **configured: the user's own** | n/a |
| 3 | Own provider | no key bound to it | not configured: that provider needs a key | the user |
| 4 | Follow the organisation | complete; confirmed | **configured: the organisation's** | n/a |
| 5 | Follow the organisation | complete; not confirmed | not configured: your organisation provides AI; confirm to use it | the user, in one step |
| 6 | Follow the organisation | no organisation, or not complete | not configured: nothing configured | the user (own key) and, in an organisation, its admin; **only the admin** when the organisation forbids personal keys |
| 7 | A removed provider (Ollama) | organisation complete | not configured: Ollama is no longer supported; your organisation provides AI, choose it in Settings | the user |
| 8 | A removed provider (Ollama) | otherwise | not configured: Ollama is no longer supported | the user and, in an organisation, its admin |

Consequences, stated so nobody has to derive them:

- **Off wins.** A user who turned AI off is never sent through the
  organisation's account, whatever the organisation sets, including when it
  forbids personal keys (parent D-9; owner O-12 does not override a user's
  "off", which sends nothing).
- **A personal key wins unless forbidden** (owner O-6, narrowed by O-12). When
  personal keys are allowed, rows 2 and 3 apply to an own-provider user even
  if the organisation is complete: nobody is moved to the organisation's
  account silently (parent AC-27).
- **Forbidding never moves anyone to the organisation's account** (P2-D2,
  factory default pending the owner, OQ-P2-2). A member who chose their own
  provider, or is stored on a removed one, gets row 10 or 11 and nothing is
  sent until they themselves choose "Use my organisation's AI" and confirm.
  Only then are they in rows 4 to 6.
- **Forbidding never rewrites the user's choice or key.** Rows 10 and 11 are
  decided from the organisation's setting at the moment of the request; the
  user's stored provider and keys are untouched. When the administrator allows
  personal keys again, the same user is back in row 2 or 3 with no action. (A
  member who chose "Use my organisation's AI" meanwhile stays on it: that was
  their own choice.)
- **While forbidden, no personal key is sent** by any request decided after
  the forbid is stored: not a stored one, not one typed into "Test
  connection" (§3.5). A request already decided before the forbid was stored
  may complete as decided; the forbid does not reach into a request in
  flight (revision 2, challenge non-blocking 3).
- **Row 4 sends only the organisation's values, of the confirmed version**:
  the provider, key and destination exactly as they stand at the version the
  member's confirmation names, with the organisation's model as it stands in
  that same read (the model is not part of a version, §3.4), and nothing
  from the member's account (parent D-18, unchanged). The configuration and
  the confirmation are read together, as one consistent state: a request
  never sends a provider, key or destination of a version other than the
  confirmed one, and never combines values of two versions or values from
  two reads. If the configuration has moved on since the confirmation (an
  administrator changed it before or while this request was decided), the
  request answers row 5 and sends nothing (revision 1, challenge blocking 2;
  revision 2, challenge non-blocking 1).
- The row is still decided before any destination approval check (parent §3):
  only rows 2 and 4 go on to it. In row 4 a destination refusal is about the
  organisation's configuration: its sentence says the organisation's AI
  destination is not approved on this deployment and that an administrator of
  the organisation can fix it, and never tells the member to change their own
  Settings (revision 1, challenge non-blocking 5).
- A failed read of the caller's settings, organisation membership,
  organisation configuration or confirmation is a server error and nothing is
  written, fetched or sent (parent §3, extended to the two new reads).
- **The organisation's administrator is named whenever the caller is in an
  organisation** (rows 6, 8 and 11, as the table says), whether or not this
  deployment currently has a way to prove one (a configured Google or
  Microsoft step-up, or a break-glass list). That is the operator's
  configuration, not the member's state, and it can change without the
  member doing anything; an administrator who then finds no way to prove it
  gets PAC-50's answer for an unconfigured step-up provider. Factory-assumed,
  P2-D12 (revision 1, challenge non-blocking 8).

### 3.1a No key leaves through error text

Provider and gateway error text is the one place a key can arrive from
outside (§2.5): a gateway may echo the key it was given, verbatim or in a
reversible encoding (JSON-escaped, percent-encoded, base64). Two rules
(revision 2, challenge revision 1 blocking 1; option (a), an orchestrator
decision at factory level, P2-D16):

- **What a member can see from a request on the organisation's
  configuration carries no provider or gateway error text at all.** When a
  request in row 4 (a parse, a brief refresh, a probe) fails at the
  provider or gateway, every member-visible text it produces (every answer,
  in every status, and the stored brief text that the brief read returns
  afterwards) is a fixed sentence written by Paceday, such as "The
  organisation's AI provider returned an error.", and contains nothing the
  provider or gateway returned. So no encoding of the organisation's key can
  reach a member through it, whatever the gateway does. (A parse failure and
  a probe failure already answer with fixed text, §2.5; the brief's failed
  text is the path this rule changes.)
- **What goes to operators is redacted of the keys sent.** The provider or
  gateway error text goes only to the server log and the error reporter
  (Sentry), and, before it does, every occurrence of every key sent with
  this request (which, after an administrator's key rotation during the
  request, is the old key, not the newly stored one) and of the caller's own
  keys (stored or submitted) is replaced by a fixed marker. No operator
  record carries provider text.

**Residual, stated exactly.** The redaction replaces exact key strings. A
gateway that echoes a key in a reversible encoding, or a fragment of it, can
therefore put that encoding into the server log or an error report. That
residual is limited to operator-only surfaces (the server log and Sentry);
it never reaches a member, an answer of any operation, a stored brief text,
or an operator record.

**Requests on the member's own key** (row 2) keep Part 1's behaviour: the
brief's failed text still carries the provider's error text, with the
member's own keys replaced by the marker, because the only key such a text
can contain is the member's own, so an encoded echo exposes nothing the
member does not already hold, and it is the member's only diagnosis of a
misconfigured key or gateway. Extending the fixed sentence to own-key
requests is not trivial in effect (it removes that diagnosis), so it is not
done here.

Text the model itself produces on success (a ready brief, a parse result, a
probe's reply) is not error text and is shown as in Part 1, with the exact
redaction above; a destination that deliberately returns a key as its
completion is an operator-approved destination misbehaving, not a case this
part covers.

### 3.2 The organisation's configuration

An organisation has at most one AI configuration: a provider (one of the five
key-based providers of Part 1), a model, the destination values that provider
needs (an optional approved gateway for OpenAI and Anthropic; endpoint,
deployment and API version for Azure OpenAI; region for Bedrock; none for
Vertex), one key bound to that provider, and whether personal keys are
allowed (allowed until an administrator says otherwise). The organisation's
destination values are subject to the operator's approval exactly as a user's
are (Part 1 §6), and, because every member can read them, to one stricter
rule (revision 2, challenge non-blocking 7, P2-D18): an organisation
destination address carries no query string, no fragment, no credentials
before the host, and no path other than what the operator approved (for a
gateway, exactly the approved path prefix; for an Azure endpoint, no path at
all). Such an address is refused when the administrator saves it, before
the proof is spent. The web also tells the administrator, next to the
destination fields, that every member can read them.

**Reading it.** Every member can read it, and every member reads the same
thing: the provider, the model, the destination, whether a key is set (never
the key), whether personal keys are allowed, a version number that changes
when the destination of members' data may have changed (§3.4), when it was
last changed, and that an organisation administrator is who can change it.
Two members, one of whom just changed it, get identical answers (parent D-7).
A user with no organisation cannot read any organisation's configuration, and
a member of one organisation can never read or change another's.

**Changing it.** Setting, replacing or removing the provider, model,
destination, key, or the personal-keys setting requires PAC-50's
administrator proof, **once per change** (owner O-11): either a fresh step-up
that proved the caller administers the organisation's Google Workspace domain
or Microsoft Entra tenant, **started for this purpose**, which authorises
exactly one change and expires after 10 minutes; or an address on the
operator's break-glass list whose session was issued less than 10 minutes ago
(PAC-50 AC-14a), which authorises **one change per fresh sign-in**: a second
change with the same session is refused with "sign in again", even inside the
10 minutes (factory-assumed, P2-D13: the conservative reading of the owner's
"repeated per change", which names the break-glass list alongside the
step-up; pending the owner, OQ-P2-4). This holds under concurrency: two
changes sent at the same time with the same session cannot both succeed;
checking that the session has not been used and recording its use are one
indivisible step (revision 2, challenge non-blocking 5). A session is known
only by its user and its issue time, which has one-second resolution; two
sign-ins by the same person within the same second therefore count as one
session, and the second is refused "sign in again". That fails safe.
A step-up proof obtained for an SSO change cannot be spent on
the AI configuration, and the reverse (PAC-50 AC-27). A member who is not
proven is refused, told why, and told how to give the proof. Being a team owner
proves nothing.

Rules for a change (factory-assumed, §9):

- Changing the provider replaces the whole configuration: the change must
  carry a key for the new provider, and every destination value not given is
  emptied. A key is never carried from one provider to another.
- A value that does not apply to the provider (an Azure endpoint for OpenAI)
  is refused, not ignored.
- Removing the provider removes the whole configuration, key included.
  Removing only the key leaves the rest; members following the organisation
  then get "nothing configured" naming the admin. Removing does not reset
  the version: versions are never reused within an organisation (§3.4).
- A change names the version of the configuration the admin was shown. If
  the version has advanced since (another administrator saved a change of
  provider, key or destination first), the change is refused as a conflict,
  nothing is stored, the answer carries the current configuration, and the
  proof is not spent (revision 1; contract challenge B-1: otherwise a key
  typed for one provider could be stored as another provider's key). This
  holds when the two changes run at the same time: the check that the
  version is still the one named and the storing of the change are one
  indivisible step, which also covers an organisation's first
  configuration, and spending the proof is part of that same step, so a
  change refused as a conflict never spends it (revision 2, contract
  challenge revision 2 B-1′). A change of only the model or only the
  personal-keys setting does not advance the version (§3.4), so it is not a
  conflict for another administrator's change; for those two values the
  later change wins.
- A refusal of the change itself (malformed, a value that does not apply, an
  unapproved destination, a stale version) happens before the proof is spent,
  so the admin can correct it without a new step-up. A refusal that can be
  decided from the change alone comes before the version check; a refusal
  that depends on what is stored (a value that does not apply to the stored
  provider, a provider change without a key) comes after it, so a stale form
  is told "the configuration changed" rather than given a misleading refusal
  (revision 2, contract challenge revision 2 non-blocking 3).
- A storage failure while storing the change leaves the configuration
  unchanged and the proof unspent (a break-glass session is not marked
  used): spending the proof and storing the change succeed or fail
  together (revision 2, consequence of B-1′; revision 1 said the proof was
  spent).
- An administrator may forbid personal keys while the configuration is not
  complete (P2-D3, factory-assumed, pending the owner, OQ-P2-3). Members other
  than those who chose "off" then have no AI until it is complete (row 11, and
  row 6 naming only the admin). The web warns the admin before saving (§3.6).
- Every change and every step-up started for it is recorded for operators:
  who, when, how it was authorised (step-up or break-glass) and which values
  changed, never the key (as PAC-50 records SSO changes).

### 3.3 The administrator step-up gains a purpose

The step-up from PAC-50 is started for a stated purpose: an SSO change (the
default, so PAC-50's flow and its clients are unchanged) or an organisation-AI
change. The purpose travels in the signed state, the resulting grant carries
it, and the page the user lands on after the identity provider says which
purpose the outcome is for when it is not the SSO default, so the web can
return the admin to the right section. The scopes requested and the checks
made are PAC-50's, unchanged; only the purpose differs. What a user loses
without the step-up is stated per purpose: for this one, that no organisation
AI change can be made except by a break-glass operator.

### 3.4 Telling the member, and asking once (owner O-13)

Before a member's first AI request that would use the organisation's account
(row 4), the member is told, and asked to confirm, that their text and
meeting details (event titles, attendee emails, and the Slack and Notion
excerpts used for briefs) are sent to their organisation's AI provider account,
named, which its administrators can **access** outside Paceday. The wording
never says the administrators can "read" (or "see") the data. Until the member
confirms, nothing is sent (row 5; and row 10, where nothing is sent until the
member chooses "Use my organisation's AI", which shows this notice).

- **What is stored.** The confirmation is stored per user, per organisation,
  and per **version** of the organisation's configuration.
- **When the version changes** (P2-D4, factory-assumed, pending the owner,
  OQ-P2-1). The version changes whenever the provider, the key or any
  destination value changes, because any of those may mean another provider
  account with other administrators; it does not change when only the model
  or the personal-keys setting changes. A member whose confirmation is for an
  older version is asked again. This **widens** the owner's "tell and ask
  once" (2026-10-28): the owner did not say when to ask again, and asking
  again after a key or destination change is the factory's choice, not the
  owner's.
- **A version is never reused within an organisation** (revision 2,
  challenge blocking 2; an orchestrator decision at factory level, P2-D17),
  including across removing the configuration and setting a new one: the
  organisation's version count survives removal, so a configuration set
  after a removal has a version no earlier configuration of that
  organisation had. A confirmation of a removed configuration therefore
  never matches a later one, and the member is asked again.
- **What a version fixes.** A version fixes the provider, the key and the
  destination values. The model is not part of it: a request sends the
  model as it stands when the request reads the configuration (§3.1). The
  personal-keys setting is not part of it either (revision 2, challenge
  non-blocking 1).
- **What a confirmation covers.** A request in row 4 sends exactly the
  provider, key and destination of the version the stored confirmation
  names, with the model read in the same consistent state; when the stored
  configuration's version is not the confirmed one, the request answers
  row 5 and sends nothing (§3.1).
- **Confirming names a version.** A confirmation names the version the member
  was shown. If the configuration changed in between, the confirmation is
  refused, nothing is recorded, and the member is shown the current
  configuration to confirm instead. A confirmation for an organisation with
  no complete configuration is refused. When both apply (the version is stale
  and the current configuration is not complete), the answer is "nothing to
  confirm", carrying the current configuration.
- **Only the confirmation records a confirmation.** Choosing "Use my
  organisation's AI" in Settings shows the same notice and, on the member's
  confirm, confirms **the version that was displayed**, through the same
  confirmation and under the same stale-version rule. A settings write by
  itself, including one that stores "follow my organisation" (and including
  a whole-document write that a client sends when its cache is empty),
  records no confirmation.
- **Choosing one's own provider, or "off", ends the confirmation.** A settings
  write that stores "off" or one of the five providers as the member's choice
  deletes the member's stored confirmation for their organisation. A member
  who comes back to "Use my organisation's AI" is told and asked again, even
  if the configuration has not changed. This is what makes §5's "withdrawing
  by choosing off or one's own provider" true (revision 1, challenge
  blocking 1).
- One member's confirmation never confirms another member.
- **No path but the confirmation operation records one.** No MCP tool, and no
  server-side path other than the member's own call to the confirmation
  operation, records a confirmation. (The server cannot tell whether a
  person or a program made that call; the web is the only client that
  offers it, and §3.7 keeps it out of MCP.) An assistant acting through MCP
  gets the "confirm in Paceday" answer and sends nothing.
- Brief refresh in rows 5, 10 and 11 is the not-configured answer of Part 1:
  nothing written, nothing fetched from Slack or Notion, nothing sent (parent
  D-12).

### 3.5 Test connection

- A probe that names a provider (one of the five: OpenAI, Anthropic, Azure
  OpenAI, Bedrock, Vertex) is always a test of the user's **own**
  configuration for that provider (parent D-19). This replaces the parent §3
  clause "or names exactly the organisation's provider and no model or key",
  which let a named provider select the organisation's key (factory-assumed,
  §9). Naming "follow my organisation", "off" or the removed provider is not
  naming a provider.
- A probe that names nothing, or names "follow my organisation", is decided
  by the table like a parse. In row 4 it tests the organisation's
  configuration exactly as members' requests use it (the confirmed version).
  A model or key in such a probe is refused (it would otherwise be silently
  ignored or, worse, combined with the organisation's key; parent D-18), and
  nothing is sent.
- While the organisation forbids personal keys, a probe that names a provider
  or carries a key is answered "personal keys are not allowed" naming the
  admin, and nothing is sent. This answer wins over every other probe
  answer except a malformed request: in particular, a confirmed follower's
  probe that names nothing and carries a key gets "personal keys are not
  allowed", not the refusal of a key with the organisation's configuration
  in the previous bullet (revision 2, challenge non-blocking 6). A probe
  that names nothing and carries no key, for a member stored on their own
  provider, answers row 10 or 11.
- An administrator who wants to test a key before saving it for the
  organisation does so with their own key (parent §5, unchanged); while
  personal keys are forbidden, that is not possible, which the admin accepted
  by forbidding them.

### 3.6 What the web shows

- **Settings, organisation AI section**, for members only, identical for
  every member: provider, model, destination, "a key is set" or not,
  whether personal keys are allowed, and "an administrator of your
  organisation can change this". An "Edit" action explains the step-up (the
  scopes and what is lost without it, from PAC-50) before navigating, sends
  the admin through it for this purpose, and on return offers the form; a
  break-glass operator with a fresh session saves directly, once per sign-in.
  A refused save says why and how to give the proof, and the section is
  unchanged. A save refused because another administrator changed the
  configuration first shows the current configuration and keeps the proof
  for the next attempt. The key field is write-only. The destination fields
  say that every member can read them (§3.2). Forbidding personal keys
  while the configuration is not complete shows a warning first. A user with
  no organisation sees no such section.
- **Settings, the user's own choice.** Members are offered "Use my
  organisation's AI" (choosing it shows the §3.4 notice and, on confirm,
  confirms the displayed version). A member stored as "follow my
  organisation" sees that choice selected and enabled, no longer the
  disabled "No provider chosen". A user with no organisation keeps Part 1's
  display (parent D-21). When the organisation forbids personal keys, the
  own-provider choices are shown disabled with the reason, and the user's
  stored choice and keys are shown as kept.
- **Dashboard bar and Settings notice.** In row 5 the notice is the §3.4 text
  with a confirm action; after confirming, the request the user made is
  sent. In row 10 the notice says personal keys are forbidden by the
  organisation, that the member's own provider and key are kept but not
  used, and offers "Use my organisation's AI" (which shows the §3.4 text and
  confirms); nothing is sent before that. In rows 6, 8 and 11 the notice names
  who can fix it as the table says (the admin appears only in an
  organisation, and alone when personal keys are forbidden).
- **Step-up landing.** An outcome for this purpose brings the admin back to
  the organisation AI section and states the outcome there, and the SSO
  step-up section shows no outcome for it; an SSO outcome lands exactly as
  today and the organisation AI section shows nothing for it.

### 3.7 What MCP sees

Nothing new is added to MCP. `clockwise_parse_command` returns the parse
answer as today, so in rows 5, 10 and 11 the assistant receives the
not-configured sentence ("confirm in Paceday …" or "ask your organisation's
administrator …") and the machine-readable reason, and nothing is sent to any
provider. `clockwise_get_settings` returns the settings document, whose AI
status carries the new rows' reasons. No MCP tool reads or changes the
organisation's configuration or gives a confirmation: the confirmation is the
person's, and the owner's rule is that nothing is sent before *the user*
confirms (factory-assumed that an assistant must not confirm for them, §9).
The MCP server passes the backend's answers through and carries no per-user
identity of its own (`mcp/client.go:13-88`), so what MCP returns is proven against
a stand-in backend, and that nothing is sent rests on P2-AC-29.

### 3.8 API surface and audit impact (what changes, in outline)

New: the organisation-AI configuration read (members), its change (proven
admins, naming the version they were shown), and the member confirmation.
Changed: the not-configured answer of parse, brief refresh, probe and
settings read (one new reason, the organisation's admin as an enabler, rows
4 to 8, 10 and 11 now reachable); the probe's rules (§3.5); the settings
write (choosing "off" or one's own provider ends the confirmation); the
step-up start, its options read and its landing (a purpose); provider error
text (never shown to a member for a request on the organisation's
configuration, and redacted of the keys sent and the caller's keys before it
is logged or reported); the organisation's change refuses a destination
address with a query, credentials or an unapproved path. The
register of hand-written versus generated endpoints gains three rows,
implemented on the generated server interface (factory README §4: new
endpoints are generated). No audit finding is opened or closed; PAC-50 AC-27
must stay true.

## 4. Acceptance criteria

### 4.0 Mapping from the parent spec

| Parent | Here |
|---|---|
| AC-22 | P2-AC-1 |
| AC-23 | P2-AC-2, P2-AC-3 |
| AC-24 | P2-AC-7 |
| AC-25 | P2-AC-8 |
| AC-26 | P2-AC-10 |
| AC-27 | P2-AC-11 |
| AC-28 | P2-AC-14 |
| AC-29 | P2-AC-15 |
| AC-30 | P2-AC-13 |
| AC-31 | P2-AC-30 |
| AC-36 | P2-AC-12 |
| AC-38 | P2-AC-28 (rewritten: §3.5) |
| AC-40, Part 2 rows | P2-AC-29 |
| AC-41 | P2-AC-22 |
| AC-21, "from Part 2" sentence | P2-AC-31 |
| (new, O-12) | P2-AC-17 to P2-AC-21 |
| (new, O-13) | P2-AC-23 to P2-AC-27 |
| (new, revision 1) | P2-AC-36 to P2-AC-41 |
| (new, revision 2) | P2-AC-42 |

### 4.1 Changing the organisation's configuration (O-11)

P2-AC-1. Given a member of organisation A who holds no live organisation-AI
proof and is not a fresh break-glass address, when they set, replace or remove
A's provider, model, destination, key or personal-keys setting, then the
request is refused as not permitted, the answer says an administrator step-up
for this purpose is needed and how to start it, and A's configuration (every
value, its version and its last-changed time) is unchanged. A member who owns
a team gets the same answer. A user with no organisation is refused as having
no organisation, with no step-up offered. [contract] [unit]

P2-AC-2. Given a person who completed the administrator step-up for A **for an
organisation-AI change**, when they set A's provider, model, destination and
key, then the change succeeds and its answer equals what any member then reads;
a second change without a new step-up is refused as in P2-AC-1. Given a live
grant obtained for an SSO change, when they change A's AI configuration, then it
is refused as in P2-AC-1 and that grant is still live (an SSO write then
succeeds with it); given a live organisation-AI grant, an SSO write is refused
and that grant is still live. Given an address on the operator's break-glass
list whose session was issued 9 minutes ago, the change succeeds; at 10 minutes
or more, or with a session stamped more than one minute in the future, it is
refused with "sign in again". Given that break-glass address made one change
with a session issued 2 minutes ago, when it sends a second valid change with
the same session (still under 10 minutes), then the second change is refused
with "sign in again" and A's configuration is as the first change left it;
after a new sign-in, one further change succeeds (P2-D13). Given that
break-glass address with a fresh, unused session, when it sends two valid
changes at the same time (a test store that holds the first change between
its check and its record until the second has made its check), then exactly
one succeeds and the other is refused with "sign in again" (revision 2).
[unit]

P2-AC-3. Given a step-up started for an organisation-AI change that succeeds,
then the grant it leaves can authorise an organisation-AI change and not an SSO
write, and the landing page's address says the outcome is for that purpose.
Given a step-up started with no purpose, then everything is as in PAC-50: the
grant is an SSO grant and the landing address is byte-identical to today's
(`?stepup=granted`, `?stepup=denied&reason=<r>`). An unknown purpose is
refused before any cookie is set. [contract] [unit]

P2-AC-4. Given a live organisation-AI grant, when the admin sends a change
that is refused on its own merits (not an object, an unknown or `null`
property, no property at all, a provider change without a key, a value that
does not apply to the provider, a destination the operator has not approved,
a key with no provider, an organisation destination address with a query
string, credentials or a path the operator did not approve), then the answer
names the problem, A's configuration is unchanged, and the grant is still
live. Given A has never had a configuration (version 0), a change that
does not name the version it was made from is refused as malformed and
nothing is stored: a missing version is not read as version 0. Each kind
of bad version (missing, not a whole number, below 0, null) gets exactly
one message, whatever else is wrong in the change (revision 2). [unit]

P2-AC-5. (Revision 2: rewritten, consequence of contract challenge
revision 2 B-1′.) Given a live grant and a change that is valid, when
storing the change fails, then the answer is a server error with no
detail, A's configuration is unchanged, and the grant is still live (a
retry of the same change then succeeds with it). Given a fresh break-glass
session instead, the session is still unused after the failure. [unit]

P2-AC-6. Given any successful change, then one operator record says who
changed it, when, whether by step-up or break-glass, and the names of the
values that changed; given any step-up started for this purpose, its outcome
record names the purpose. No record contains the key. [unit]

### 4.2 Reading it

P2-AC-7. Given A has a key set, when two members of A, one of whom has just
changed the configuration with a proof, each read A's configuration, then the
two answers are byte-identical and carry the provider, the model, the
destination, that a key is set, whether personal keys are allowed, the
version, the last-changed time and that an organisation administrator can
change it. And, with A's key a distinctive string, none of these answers
contains it, in any status the operation can give: the organisation
configuration read; every answer of its change (success, each refusal,
conflict, server error); every answer of the confirmation; the settings read;
the parse answer; every probe answer; the brief refresh answer and the brief
read afterwards. (P2-AC-39 covers the case of a provider that echoes the key.)
[contract] [unit] [e2e]

P2-AC-8. Given a user in organisation B, when they try to read or change A's
configuration or confirm it by any means the contract offers, then what they
read or change is B's (there is no way to name another organisation), and
nothing about A is returned. [unit]

P2-AC-9. Given a user with no organisation, when they read the organisation
configuration or confirm, then each is refused as having no organisation, and
their settings read says they are in no organisation. [unit]

### 4.3 Which configuration a request uses

P2-AC-10. Given A complete, member M1 who follows A and has confirmed, and
member M2 with their own complete configuration (personal keys allowed), when
each parses a sentence, then M1's request carries A's key to A's provider and
M2's carries M2's key to M2's provider, and no request carries A's key for M2
or M2's key for anyone else. [unit]

P2-AC-11. Given M2 of P2-AC-10 removes their own key, when M2 parses, then the
answer is "needs a key" for M2's provider (row 3) and nothing is sent, until
M2 chooses "Use my organisation's AI". [unit]

P2-AC-12. Given A configured for Azure OpenAI with approved endpoint E_A,
deployment D_A and key K_A, and a confirmed follower M1 who has stored a
different approved Azure endpoint, another deployment, a Bedrock region and an
approved OpenAI gateway on their own account, when M1 parses and runs the
probe naming nothing, then every request goes to E_A's chat operation for D_A
carrying K_A, the other destinations receive nothing, and no request combines
K_A with any value of M1's account. Given instead A on OpenAI with no gateway
and M1 with a stored approved gateway, M1's request goes to OpenAI's public
host. [unit]

P2-AC-13. Given A complete and an admin who removes A's key, when a confirmed
follower next parses, then the answer is "nothing configured" naming the user
and the organisation's admin, nothing is sent, and a member with their own key
is unaffected. [unit]

P2-AC-14. Given an account created after Part 1 (stored as "follow my
organisation") in A, when A has no configuration, then it answers "nothing
configured" naming the user and the organisation's admin (row 6). When A
becomes complete, then the account answers row 5 until the user confirms and
row 4 after, without the user opening Settings. [unit] [e2e]

P2-AC-15. Given an account stored on Ollama since before Part 1, when A becomes
complete, then it answers row 7 and nothing is sent until the user chooses.
Given an account stored on Bedrock with no key, it answers "needs a key" for
Bedrock before and after A is complete. [unit]

P2-AC-16. Given an account stored as "off", when A is complete, forbids
personal keys, and the user has or has not confirmed, then every AI request
answers "off" (row 1) and nothing is sent. [unit]

### 4.4 Forbidding personal keys (O-12)

P2-AC-17. Given a member without a proof, when they change only the
personal-keys setting, then it is refused as in P2-AC-1; with a proof it
succeeds and spends the proof. [unit]

P2-AC-18. (Revision 1: rewritten for P2-D2's current default.) Given A
complete and forbidding personal keys, and member M2 stored on OpenAI with a
key K2, whether or not M2 has ever confirmed A, when M2 parses, refreshes a
brief or runs the probe naming nothing, then each answers row 10 naming only
the user, its sentence says personal keys are forbidden by the organisation,
that M2's own provider and key are kept but not used, and to choose "Use my
organisation's AI" in Settings; nothing is sent, and K2 is never sent. When
M2 confirms A's current version without changing their choice, the next
parse still answers row 10 and sends nothing. When M2 chooses "Use my
organisation's AI" and confirms the displayed version, the next parse uses
A's configuration (row 4). M2's stored OpenAI key is still stored throughout.
Given instead M2 never changes their choice, when the admin allows personal
keys again, M2's next parse carries K2 to OpenAI with no action by M2. [unit]

P2-AC-19. Given A forbidding personal keys with no key set, when M2 of
P2-AC-18 parses, then the answer is row 11 naming only the organisation's
admin; when a follower parses, the answer is "nothing configured" naming only
the organisation's admin; nothing is sent in either case. [unit]

P2-AC-20. Given A forbidding personal keys, when a member's probe names
OpenAI with no key, names Anthropic with a key, or names nothing and carries a
key, then each answers "personal keys are not allowed" naming the
organisation's admin and no request reaches any host. This includes a
confirmed follower of a complete A whose probe names nothing and carries a
key or names "follow my organisation" and carries a key: the answer is
"personal keys are not allowed", not the refusal of P2-AC-28 (revision 2).
[unit]

P2-AC-21. Given A forbidding personal keys, when a member chooses their own
provider and saves a key for it, then the save succeeds, their settings read
reports the key on file and AI status row 10 or 11, and no AI request sends
that key while the setting stays on. [unit]

### 4.5 Telling and asking once (O-13)

P2-AC-22. Given a new account in A, A complete, and the user not confirmed,
when they use the natural-language bar, then nothing is sent, the answer is
row 5, and the page shows a notice naming A's provider and saying that their
text and meeting details (event titles, attendee emails, Slack and Notion
excerpts) go to the organisation's AI provider account, which its
administrators can access outside Paceday, with a confirm action. After they
confirm, the request is sent with A's configuration, and the notice is not
shown again on later requests. Choosing "Use my organisation's AI" in Settings
shows the same notice and, on confirm, confirms the version displayed
(P2-AC-38). [unit] [e2e]

P2-AC-23. Given a member confirmed for A's current configuration, when an
admin changes A's provider, or its key, or any destination value, then the
member's next request answers row 5 and sends nothing until they
confirm again; when the admin changes only the model or only the personal-keys
setting, the member's next request is sent without asking. [unit]

P2-AC-24. Given a member shown A's configuration at one version, when the admin
changes it (advancing the version) and the member then confirms the version
they were shown, then the confirmation is refused, nothing is recorded, the
answer carries A's current configuration, and the member's next request still
answers row 5. Given A with no complete configuration, a confirmation is
refused and nothing is recorded. Confirming the current version twice
succeeds both times. [contract] [unit]

P2-AC-25. Given members M1 and M3 of A, when M1 confirms, then M3's next
request still answers row 5. [unit]

P2-AC-26. Given every not-configured answer the server can give (every row and
every probe case), the web notice for rows 5 and 10, and the notice shown when
a member chooses "Use my organisation's AI", when a test reads each sentence,
then the row 5 sentence and both web notices that ask for confirmation
(row 5's, and the one shown on choosing "Use my organisation's AI") contain
"access", and none of the sentences contains the word "read", "reads" or
"see" (as whole words, case-insensitive). [unit] [e2e]

P2-AC-27. Given a member in row 5, 10 or 11 and, for one event, each stored
brief state in turn (none, pending, ready, failed), when they refresh that
brief, then the answer is not-configured, the brief read afterwards answers
what it answered before, and no Slack, Notion or provider request is made.
[unit]

### 4.6 Test connection

P2-AC-28. Given a confirmed follower M1 of A (A on OpenAI with key K_A), with
no keys of their own and personal keys allowed: when M1's probe names
nothing, or names "follow my organisation", then it tests A's configuration
and K_A goes only to A's destination; when it names OpenAI with no key, it
answers "needs a key" for OpenAI and sends nothing (naming the organisation's
provider does not select the organisation's key); when it names Anthropic with
a key, only that key is sent, to Anthropic; when it names nothing or "follow my
organisation" and carries a model or a key, it is refused and nothing is sent.
An unconfirmed follower's probe naming nothing answers row 5 and sends
nothing. [unit]

### 4.7 One state, one answer

P2-AC-29. Given one account per reachable row of §3.1 (rows 1 to 8, 10 and
11; row 9 is reserved), when each parses, refreshes a brief, runs the probe
naming nothing and reads its settings, then all four give exactly that row's
outcome, reason and who-can-fix-it, and only rows 2 and 4 send a request.
[unit]

### 4.8 Web and MCP

P2-AC-30. Given A complete and two members (one an admin), when each opens
Settings, then the organisation AI section shows the same provider, model,
destination, "a key is set", personal-keys setting and "an administrator can
change this". When a member presses Edit, the page explains the step-up before
navigating and sends them through it for this purpose; on a granted return the
form is offered, and saving succeeds once; a member who cannot prove admin
status sees the refusal and who can change it, and the section is unchanged.
A break-glass operator with a fresh session saves without a step-up. The key
field never shows a key. The destination fields say every member can read
them. Forbidding personal keys while A has no key shows a warning before
saving. A user with no organisation sees no section. [e2e]

P2-AC-31. Given a member stored as "follow my organisation", when they open
Settings, then "Use my organisation's AI" is shown selected and enabled (not
"No provider chosen"); a user with no organisation stored the same way still
sees Part 1's disabled "No provider chosen" and no organisation wording. When
A forbids personal keys, the own-provider choices are disabled with the
reason, and a member's stored own provider is shown as kept. The not-configured
notice names the organisation's admin exactly when the answer's who-can-fix-it
includes them. [e2e]

P2-AC-32. Given a step-up for an organisation-AI change, when the identity
provider returns, then the admin lands on the organisation AI section with the
outcome stated there, and the SSO step-up section shows no outcome (in
particular not "used by your next SSO configuration change"); an SSO step-up
lands and reads exactly as under PAC-50, and the organisation AI section
shows no outcome for it. [e2e]

P2-AC-33. Given a stand-in backend whose parse answer is the row 5, row 10
or row 11 not-configured answer, when the MCP tool `clockwise_parse_command`
is called, then it returns that sentence and reason unchanged; and the MCP
server's tool list has no tool that calls the organisation configuration
read or change or the confirmation. (That nothing is sent to a provider in
those rows is P2-AC-29's, on the backend; the MCP server has no per-user
identity to set up a member end to end, `mcp/client.go:13-88`.) [unit]

### 4.9 Contract

P2-AC-34. Given the assembled contract, when it is read, then: the
organisation configuration's read, change and confirmation are documented with
every refusal; the change's refusal distinguishes "no organisation",
"administrator step-up needed for this purpose" (with how to start it) and
"sign in again", and a stale version is a conflict carrying the current
configuration; the settings write documents that storing "off" or a provider
ends the confirmation and that storing "follow my organisation" records none;
the step-up start, its options read and its landing document
the purpose; the not-configured reason set contains the personal-keys reason;
and no schema carries an organisation key in a readable response. [contract]

P2-AC-35. Given the e2e stack with no key anywhere and a fresh user in an
organisation with no configuration, when they parse, then the answer is
"nothing configured" naming the user and the organisation's admin, and the
dashboard says so. [e2e]

### 4.10 Added by revision 1

P2-AC-36. (Challenge blocking 1.) Given A complete with personal keys allowed
and member M following A who confirmed A's current version V, when M chooses
OpenAI and saves a key K2 for it, and then an admin forbids personal keys (V
unchanged), then M's next parse, brief refresh and probe naming nothing each
answer row 10 and send nothing. Given instead M chooses "off" and then a
settings write stores "follow my organisation" again (without the
confirmation), M's next parse answers row 5 and sends nothing; likewise after
M chooses OpenAI and a settings write stores "follow my organisation" again.
In each case M's stored confirmation was deleted by the write that stored
"off" or OpenAI. [unit]

P2-AC-37. (Challenge blocking 2a.) Given member M following A who confirmed
version V of A (provider P_V, key K_V), and an admin change to version V+1
(provider P_W, key K_W) that is stored after M's request has begun to be
decided (a test store that applies the change at each storage access the
request makes, one run per access: before the first, between each pair,
and after the last), when the request completes, then in every run it
either sends exactly V's provider, key and destination (P_V, K_V, V's
destination) with the model read together with them, or answers row 5 and
sends nothing; it never sends any value of V+1, and never combines values
of V and V+1. Given the change was stored before the request began, the
request answers row 5 and sends nothing. Given instead an admin change of
only the model at V (the version does not advance), the request sends V's
provider, key and destination with either the old or the new model, never
a value of any other version (revision 2). [unit]

P2-AC-38. (Challenge blocking 2b.) Given member M of A, A complete at version
V, and no confirmation, when a settings write stores "follow my
organisation" (alone, or inside a whole settings document), then no
confirmation is recorded and M's next parse answers row 5 and sends nothing.
When M chooses "Use my organisation's AI" in the web while V is displayed and
confirms, then a confirmation naming V is recorded and the next parse is
sent with V's values. Given the admin changed A to V+1 after V was displayed
and before M confirmed, then that confirmation is refused, nothing is
recorded, the web shows V+1's notice, and M's next parse answers row 5.
[unit] [e2e]

P2-AC-39. (Challenge blocking 3; revision 2: rewritten for challenge
revision 1 blocking 1 and non-blocking 2.) Given a provider or gateway test
double that answers with an error body containing a distinctive marker
string X and the key it was sent, and A's key K_A and a member's own key
K_U both distinctive strings:
(i) when a confirmed follower of A refreshes a brief and the call fails,
then the brief read afterwards is the fixed sentence of §3.1a and contains
neither X nor K_A;
(ii) when that follower parses and the call fails, and when they probe
naming nothing and the call fails, then no answer, in any status, contains
X or K_A;
(iii) as (i) and (ii) with the double echoing K_A JSON-escaped (every `/`
sent as `\/`, K_A chosen to contain `/`), and again percent-encoded: the
brief read and every answer still contain no X and no form of K_A (they
carry no provider text at all);
(iv) when an admin replaces K_A with K_A' (a new version) after the
follower's request has sent K_A and before the double's echo is handled,
then no server log line and no captured error-reporter event contains K_A
or K_A';
(v) in each of (i), (ii) and (iv) with a verbatim echo, and for a member in
row 2 whose own call fails, no line written to the server log, no event
sent to the error reporter (a test reporter capturing what would go to
Sentry), and no operator record contains K_A or K_U; no operator record
contains X;
(vi) for that row-2 member, the brief read after a failed refresh contains
X (Part 1's diagnosis is kept) and not K_U. [unit]

P2-AC-40. (Contract challenge B-1; revision 2: interleaved case, contract
challenge revision 2 B-1′.) Given an admin shown A at version V and
holding a live organisation-AI grant, when another proven admin changes A to
V+1 and the first admin then sends a valid change naming V, then the change
is refused as a conflict, the answer carries A's current configuration
(V+1), A is unchanged, and the first admin's grant is still live (a change
naming V+1 then succeeds with it). Given instead two admins X and Y, each
holding a live organisation-AI grant and shown A at V, when Y's change
naming V has passed every check and X's change naming V is stored before
Y's is (a test store that applies X's change between Y's checks and Y's
storing), then Y is refused as a conflict carrying X's result, A holds
exactly X's change, and Y's grant is still live. The same holds for an
organisation with no configuration yet, with two first setups naming
version 0. A body malformed in a way decidable from the body alone and
naming a stale version is refused as malformed (the body is checked first);
a change naming a stale version that would be refused only because of what
is stored (a value that does not apply to the stored provider) is refused
as a conflict. [contract] [unit]

P2-AC-41. (Challenge non-blocking 5.) Given A complete with a destination the
operator has since stopped approving, and a confirmed follower M, when M
parses, refreshes a brief and probes naming nothing, then nothing is sent,
each refusal says the organisation's AI destination is not approved on this
deployment and that an administrator of the organisation can fix it, and no
sentence tells M to change their own Settings; M's settings read still says
configured (the settings read does not check the destination, Part 1). [unit]

### 4.11 Added by revision 2

P2-AC-42. (Challenge revision 1 blocking 2.) Given member M following A who
confirmed A's current version V, when an admin removes A's configuration
and then sets a new complete configuration (another provider and key), then
the new configuration's version is greater than every version A ever had
(in particular not V), M's next parse answers row 5 and nothing is sent to
any host; after M confirms the new version, the parse is sent with the new
configuration. The same holds when the new configuration repeats the
removed one's provider and key exactly. [unit]

## 5. Explicitly out of scope

- **Withdrawing a confirmation** other than by choosing "off" or one's own
  provider, each of which ends it (§3.4; a member can choose their own
  provider even while personal keys are forbidden, P2-D8). A separate
  "withdraw" action is a later change.
- **Testing an unsaved organisation configuration** (parent §5).
- **Per-team keys, usage reporting, quotas** (parent §5).
- **An admin role stored in Paceday** (parent §5, D-6). The proof stays
  PAC-50's, per change.
- **Telling members when the organisation changes its configuration** other
  than by asking again at their next AI request (P2-AC-23). No email or
  notification is sent.
- **Confirming through MCP** (§3.7).
- **A key echoed in a transformed form** (a fragment, an encoding) by a
  provider or gateway, **in the server log and in error reports**: only the
  exact strings of the keys sent and of the caller's own keys are replaced
  there (§3.1a). This residual is limited to those operator-only surfaces;
  member-visible text from a request on the organisation's configuration
  carries no provider text at all, so no form of the organisation's key
  reaches a member (revision 2).
- **Removing provider error text from own-key requests**: a member's own
  failed brief keeps Part 1's provider text, redacted of the member's own
  keys (§3.1a).

## 6. What must not change

- **Keys are write-only**, the organisation's included: no response, in any
  status, carries a user or organisation key (PAC-24; Part 1 §6;
  `TestLLMTest_BodyRulesAndNoKeyEcho`, `e2e/tests/settings.spec.ts`). From
  this part: no member-visible text (any answer, any stored brief text)
  carries the organisation's key in any form through provider or gateway
  error text, because a request on the organisation's configuration shows
  members no such text at all (model output on success: §3.1a); no
  operator record carries provider text; and the server log and error
  reports sent to Sentry carry no exact copy of any key sent with the
  request or of the caller's own keys (§3.1a, P2-AC-39). An encoded or
  partial echo can still reach the server log or Sentry; that residual is
  operator-only (§5). Today only the probe redacts, and only the caller's
  own keys (§2.5), so this is a widening, not a preservation.
- **The probe body cannot carry a destination** (PAC-24 invariant).
- **Approved destinations** apply to the organisation's values exactly as to a
  user's (PAC-66 A and B, Part 1 §6).
- **PAC-50 is unchanged for SSO.** A step-up started without a purpose issues
  an SSO grant and lands exactly as today; the SSO write gate (including its
  break-glass rule, which keeps allowing several SSO writes within one fresh
  session: P2-D13 applies to organisation-AI changes only), its 403 shapes
  and its option read without a purpose are byte-identical. Protected by
  PAC-50's `TestPAC50_*` tests and AC-27 (a grant of another purpose is never
  spent by an SSO write).
- **Part 1 for users with no organisation** is unchanged: rows 1, 2, 3, 6 and
  8 with the user alone as the fixer.
- **"Off" is never overridden** (parent D-9).
- **Settings writes validate only what they change** (PAC-24 AC-17). Storing
  "off" or a provider additionally ends the caller's confirmation (§3.4);
  that is a side effect, not a validation, and never refuses the write.

## 7. Migration and rollback

**Migration (up).** Adds storage for (a) one AI configuration per
organisation: provider, model, destination values, the key, whether personal
keys are allowed, the configuration version, and when and by whom it was last
changed; the organisation's version count is kept when the configuration is
removed, so a version is never reused within an organisation (P2-D17;
removing empties the configuration's values rather than forgetting the
count); (b) one confirmation per user and organisation: the version confirmed
and when; (c) a new kind of operator record for organisation-AI changes,
which for a break-glass change also identifies the session used (user and
issue time), at most one per session, so that a second change with the same
session is refused even when both arrive at once (P2-D13). No existing row
is rewritten: settings rows, including every stored "follow my organisation"
choice, are untouched; PAC-50's grant table needs no change (its purpose
column takes the new value). Until an admin sets a configuration, every
organisation behaves as in Part 1 except that rows 6 and 8 in an
organisation now also name its admin.

**Down.** Drops (a), (b) and (c), and deletes grants issued for the new
purpose (useless without this part) and operator records of the new kind
(required before the record kind can be removed). **This loses every
organisation's AI configuration including its key, every confirmation, and the
operator records of organisation-AI changes.** Release notes must say so.
After rollback, followers answer "nothing configured" (Part 1), members whose
organisation forbade personal keys are back on their own keys (their choice
and keys were never rewritten), members who chose "Use my organisation's AI"
answer "nothing configured", and nobody is asked to confirm; after
re-upgrading, admins re-enter the configuration and members confirm again.

**Code rollback without the down migration is not safe for O-12** (revision
1, challenge non-blocking 6). Part 1's code ignores the new storage, so it
also ignores "personal keys forbidden": members on their own provider have
their personal keys sent again while the stored setting still says
forbidden, and nothing tells the administrator. Followers answer "nothing
configured" and nothing goes to the organisation's account. Release notes
must say that a code rollback re-enables personal keys in every organisation
that forbade them, and that an operator who needs O-12 to hold must not roll
back the code (or must accept that). No data is lost by a code rollback; a
re-upgrade restores the stored setting's effect.

**A code rollback also suspends "choosing off or one's own provider ends the
confirmation"** (revision 2, challenge non-blocking 4). Part 1's code does
not delete a member's confirmation when they store "off" or a provider, so a
member who leaves the organisation's AI while the code is rolled back keeps
their stored confirmation; after a re-upgrade, a later forbid or a later
return to "follow my organisation" (through a client that does not show the
notice) would use that confirmation without asking again, which is revision
0's blocking 1 coming back. So the re-upgrade after any code rollback must
delete every stored confirmation before the Part 2 code serves a request,
and every member following an organisation is asked again. Release notes
must state this step; the stage-4 api PR names it in its rollback section.

## 8. Open questions

None blocks the contract stage. All four are product questions put to the
owner on 2026-10-28 and recorded as asked in the factory's owner-decisions
record, which lives in the workspace outside this repository ("Still open",
LLM org key; OQ-P2-3 was added to that record after revision 1, challenge
revision 1 non-blocking 8). Until the owner answers, the factory default below
applies; each default is factory-assumed, pending the owner. Each question is
stated neutrally; the default is what is built, not a recommendation the
owner made.

- **OQ-P2-1 (P2-D4)** — When an administrator changes the organisation's key
  or destination (not its provider), are members told and asked again before
  their next AI request, or only when the provider changes? **Default: asked
  again on a change of provider, key or any destination value.** This widens
  the owner's "tell and ask once" (2026-10-28), which said nothing about
  asking again; the cost is a confirmation after every key rotation.
- **OQ-P2-2 (P2-D2)** — While personal keys are forbidden, is a member who
  chose their own provider moved onto the organisation's account (after
  confirming), or does that member have no AI until they themselves choose
  "Use my organisation's AI" and confirm? **Default: not moved**; they get
  row 10 ("personal keys are forbidden by your organisation; choose 'Use my
  organisation's AI' in Settings") and nothing is sent until they choose it
  and confirm. This is the conservative alternative: it sends nothing more
  and asks no less. Row 9 is reserved for the other answer.
- **OQ-P2-3 (P2-D3)** — May an administrator forbid personal keys before the
  organisation's configuration is complete (which leaves every member except
  those on "off" without AI until it is)? **Default: yes, with a warning in
  the web before saving.**
- **OQ-P2-4 (P2-D13)** — Does a break-glass operator's fresh sign-in allow one
  organisation-AI change, or any number within its 10 minutes? **Default: one
  change per fresh sign-in**, the conservative reading of "repeated per
  change".

Not a product question, recorded so a reader does not have to infer it:
the organisation's administrator is named as able to fix the configuration
even on a deployment with no way to prove one (P2-D12).

## 9. Decisions

### Owner

- **O-11 (owner, 2026-10-28, OQ-1: yes).** Setting or changing the
  organisation-wide AI provider and key requires PAC-50's admin proof
  (Google Workspace / Microsoft 365 admin step-up, or the operator's
  break-glass list), repeated per change. → §3.2, §3.3, P2-AC-1 to P2-AC-5,
  P2-AC-17, P2-AC-40. How "repeated per change" applies to the break-glass
  list is P2-D13 (factory-assumed).
- **O-12 (owner, 2026-10-28, OQ-7: yes).** An organisation admin may forbid
  personal keys, so every member uses the organisation's key. → rows 10 and
  11, §3.5, P2-AC-17 to P2-AC-21, P2-AC-36. Under P2-D2's default "every
  member uses the organisation's key" holds as "no member's personal key is
  used, and the organisation's is the only AI available"; whether a member
  is moved onto it without choosing it is OQ-P2-2.
- **O-13 (owner, 2026-10-28, OQ-8: yes).** Tell and ask once before the first
  AI request; the wording says the provider account's administrators can
  **access** the data, never "read"; nothing is sent before the user confirms.
  → §3.4, P2-AC-22 to P2-AC-27, P2-AC-36 to P2-AC-38. The owner did not say
  when, if ever, to ask again; that is P2-D4 (factory-assumed).
- Carried from the parent spec: O-1, O-5, O-6, O-7, O-8 (2026-10-27).

### Carried factory decisions (parent spec, pending owner confirmation)

D-6 (now confirmed by O-11), D-7, D-8, D-9, D-12, D-15 (confirmed by O-13),
D-18, D-19 (amended by P2-D6).

### Assumed by the factory, pending owner confirmation

Product-level (put to the owner, §8): P2-D2, P2-D3, P2-D4, P2-D13.
Interface or engineering choices: the others. P2-D16 to P2-D18 are listed
separately below as the orchestrator's factory-level decisions (revision 2).

- **P2-D1. The organisation-AI proof is a PAC-50 grant of its own purpose,
  obtained by a PAC-50 step-up started for that purpose.** Rejected: a second
  step-up flow (a second callback address to register with Google and
  Microsoft, and two copies of the checks); reusing the SSO grant (PAC-50
  AC-27 forbids it, and an SSO admin would silently gain AI authority).
- **P2-D2 (revision 1: default changed). While personal keys are forbidden,
  an own-provider or removed-provider member is NOT moved to the
  organisation's configuration** (row 10 when complete, row 11 when not).
  Nothing is sent for them until they choose "Use my organisation's AI" and
  confirm the displayed version. Their choice and keys are not rewritten.
  Pending the owner, OQ-P2-2. Rejected for now: moving them once they confirm
  (revision 0's default; it moves a member who deliberately chose their own
  provider onto the organisation's account, which is the owner's call, not
  the factory's); rewriting their choice (loses it when the ban is lifted).
- **P2-D3. Forbidding is allowed while the configuration is incomplete**, with
  a warning in the web (row 11). Pending the owner, OQ-P2-3. Rejected:
  refusing it (the admin could still reach the same state by removing the key
  afterwards, so the refusal would protect nothing).
- **P2-D4. The confirmation is per user, per organisation, per configuration
  version; the version advances on a change of provider, key or any
  destination value.** This widens the owner's "tell and ask once"; pending
  the owner, OQ-P2-1. Rejected: never asking again (a new provider, key or
  gateway can be another account with other administrators, which is exactly
  what O-13 tells the member about); asking again on a provider change only
  (a new key or gateway can be another account on the same provider).
- **P2-D5. One new not-configured reason, "personal keys forbidden".** The
  confirm-required case reuses Part 1's reserved "organisation unconfirmed"
  reason rather than a new "consent required" one, so Part 1 clients already
  know it. Rejected: separate reasons for row 10 and row 11 (who-can-fix-it
  already tells them apart).
- **P2-D6. A probe naming a provider never selects the organisation's
  configuration** (replaces the parent §3 clause about naming the
  organisation's provider). The organisation's configuration is probed only
  as members' requests would use it, and a model or key with it is refused.
- **P2-D7. A provider change on the organisation's configuration replaces it
  whole and must carry a key; a value that does not apply is refused.**
  Rejected: per-provider key slots as for users (each organisation change is
  a proven, deliberate act; one configuration is simpler to show members).
- **P2-D8. Personal-key writes are accepted while forbidden** (stored, never
  sent). Rejected: refusing them (would make the settings write depend on the
  organisation's state, and the user's preference could not be kept for when
  the ban is lifted).
- **P2-D9. No MCP tool confirms or reads the organisation configuration.**
  The confirmation is the person's (O-13: "nothing is sent before the user
  confirms").
- **P2-D10. Organisation-AI changes and their step-ups are recorded for
  operators** (who, when, how authorised, which values; never the key), as
  PAC-50 does for SSO.
- **P2-D11. A member stored as "follow my organisation" (every account created
  since Part 1) is treated as a new account**: it follows the organisation,
  subject to confirmation (rows 4-6). Parent D-8 (pre-Part-1 accounts are not
  enrolled) is unchanged.
- **P2-D12 (revision 1). The organisation's administrator is named as able to
  fix it whenever the caller is in an organisation**, whatever the
  deployment's step-up or break-glass configuration (§3.1). Rejected: naming
  the admin only when the deployment can prove one (the same member state
  would then give different answers as the operator changes configuration,
  and the member would be told nobody can fix it when the operator can).
- **P2-D13 (revision 1). A break-glass sign-in authorises one
  organisation-AI change.** A second change with the same session is refused
  with "sign in again". Pending the owner, OQ-P2-4. Rejected for now: any
  number of changes within 10 minutes (PAC-50's SSO rule; the owner's
  "repeated per change" names the break-glass list alongside the step-up).
  PAC-50's SSO break-glass rule is unchanged. Revision 2: the check and
  the record are one indivisible step per (user, session issue time), so two
  concurrent changes with one session cannot both succeed; two sign-ins in
  the same second share an issue time and count as one session (fails
  safe).
- **P2-D14 (revision 1). Settings choices end the confirmation; only the
  confirmation operation records one; a request sends exactly the confirmed
  version.** (§3.1, §3.4.) Rejected: keeping a confirmation across a switch
  to one's own provider (a later forbid would send the member's data with no
  notice, challenge blocking 1); treating a settings write of "follow my
  organisation" as confirming (a client can send it without showing
  anything, challenge blocking 2).
- **P2-D15 (revision 1). An administrator's change names the version they
  were shown; a stale one is a conflict, checked before the proof is spent**
  (contract challenge B-1). Rejected: last write wins (a key typed for one
  provider could be stored as another's). Revision 2 (contract challenge
  revision 2 B-1′, an orchestrator decision at factory level): the version
  check and the storing are one indivisible conditional step, covering the
  first setup, and spending the proof is part of it, so concurrent changes
  cannot both pass and a conflict or a storage failure never spends the
  proof. Rejected: checking the version only before the proof (two
  concurrent changes both pass it).

### Decided by the orchestrator, factory-level (revision 2)

Recorded as factory decisions, not owner decisions; each answers a
blocking finding of challenge revision 1 or contract challenge revision 2.

- **P2-D16. Member-visible text from a request on the organisation's
  configuration carries no provider or gateway error text** (challenge
  revision 1 blocking 1, option (a)). A failure answers with a fixed
  sentence; the provider text goes only to the server log and Sentry,
  redacted of the keys sent with the request and the caller's own keys
  (§3.1a). Rejected: option (b), keeping the text and stating that a member
  can receive the whole organisation key in a reversible encoding (it would
  need the owner to accept a member holding the organisation's key, which
  O-7 and O-13 rule out); redacting known encodings too (an open-ended list:
  a gateway's own encoding is not knowable). Own-key requests keep Part 1's
  text (§3.1a: the only key there is the member's own).
- **P2-D17. A configuration version is never reused within an
  organisation**, including across removal and a new setup (challenge
  revision 1 blocking 2). Rejected: ending every confirmation when the
  configuration is removed (correct too, but it relies on every removal
  path deleting confirmations, while a never-reused count makes a stale
  confirmation unmatchable by construction).
- **P2-D18. The organisation's destination addresses are stricter than a
  user's**: no query string, fragment, credentials or unapproved path
  (challenge revision 1 non-blocking 7), because every member reads them.
  Rejected: only warning the admin (a secret put in the address would still
  be shown to every member).

## Challenge, revision 0 — 2026-10-28

**Verdict**: accept-with-changes

Checked by: `git fetch origin`; `git show` / `git grep` on
`origin/factory/new-llm-byo-part2` (a90ba5e), clockwise-like `origin/main`
(ae5c2cf) and smart-calendar-flow `origin/main` (eea0cf6). No test, build or
`make verify` was run for this challenge. Spec line numbers are of a90ba5e.

### Blocking

1. **A withdrawn confirmation comes back when personal keys are forbidden.**
   §5 (632-634) treats choosing one's own provider as withdrawing the
   confirmation. §3.4 (268-274) stores it per user, organisation and version
   and never clears it, and the personal-keys setting does not change the
   version (271-272). Scenario: M confirms version 3 while following A;
   M chooses OpenAI and saves K2 (allowed); the admin forbids personal keys
   (still version 3). M's next parse goes to A's account with no notice: row 9
   (156), "configured: the organisation's". P2-AC-18 only covers a member who
   never confirmed. *Resolves when* the spec says whether choosing one's own
   provider (and "off") ends the stored confirmation. Add a criterion: confirmed
   follower → own provider → admin forbids → the next parse, brief refresh and
   probe answer row 10 and send nothing. Alternatively, if the owner wants a
   confirmation to survive that switch, rewrite §5 and get the owner's answer.

2. **The confirmation is not tied to the configuration actually sent.**
   "Confirmed" (151) means "the current configuration", but nothing requires
   the values a request sends to be those of the version the stored
   confirmation names. A request that reads the confirmation, then (after an
   admin change) reads the new provider and key, sends to an account the member
   never confirmed. Separately, "Choosing 'Use my organisation's AI' in
   Settings ... counts as confirming" (280-281; P2-AC-22, 530-531) does not say
   which version is confirmed. The web sends the whole settings document when
   its cache is empty (`smart-calendar-flow/src/hooks/useSettings.ts:16`,
   `src/api/client.ts:900-902`), so `llmProvider: "organization"` can arrive
   without the member being shown anything. *Resolves when* §3.1 and §3.4 state
   that (a) a request in rows 4/9 sends exactly the values of the version its
   confirmation names, and otherwise answers row 5/10, and (b) the Settings
   choice confirms only the version that was displayed, under P2-AC-24's
   stale-version rule, and a settings write by itself records no confirmation
   (the contract already rejects a `patchSettings` confirmation flag, contract
   feature .md:93-97). Add a criterion for each.

3. **The organisation key can leak through provider error text, and P2-AC-7
   cannot be falsified as written.** "No answer of any operation, in any
   status" (438) does not say which reads to test. On `main`:
   - provider error bodies are copied verbatim (`backend/nlp/parser.go:123`);
   - a failed brief stores `LLM error: <that>` as its text
     (`backend/engine/meeting_brief_service.go:89`), which the member reads back
     (`handlers_meeting_briefs.go:110`, `briefToResponse`);
   - the probe blanks out only the caller's own keys before logging
     (`backend/api/handlers_llm.go:62-65`, 122; logs and Sentry at
     `server_errors.go:47-48`);
   - a parse failure is logged unredacted (`handlers_nlp.go:30`).
   Under Part 1 the only key in those texts was the member's own. Under Part 2
   it is a key members must never hold, and gateways (owner O-10) may echo it.
   *Resolves when* P2-AC-7 (or a new criterion) names the cases: with a provider
   or gateway that echoes the key in its error, (i) the brief read after a
   failed refresh in rows 4/9, (ii) the parse and probe answers in every
   status, and (iii) server logs, error reports and operator records never
   contain A's key. §6 should extend "keys are write-only" to logs and error
   reports for the organisation key.

4. **Break-glass is not "repeated per change" (owner O-11).** The owner's
   sentence covers the step-up *and* the break-glass list, "repeated per
   change". §3.2 (213-219) says "once per change" but lets a break-glass
   session under 10 minutes old make any number of changes. P2-AC-2 tests one
   break-glass change only. *Resolves when* the spec either requires one change
   per fresh break-glass sign-in, or records unlimited changes within the
   window as a factory assumption pending owner confirmation (a new P2-D),
   with a criterion for the second break-glass change either way.

### Non-blocking

1. **Wrong attribution, §3.4 (273-274).** "the owner's 're-ask if the
   provider changes'" appears neither in the owner's decisions (2026-10-28,
   OQ-8: "tell and ask once") nor in the parent spec. Re-asking on any change
   is the factory's widening of "once". Restate OQ-P2-1 accordingly.
2. **§8 (690-691) claims every default is the more conservative choice.** That
   is false for OQ-P2-2 / P2-D2. The alternative (stay off until the member
   chooses "Use my organisation's AI") sends nothing more and asks no less.
   P2-D2 moves a member who deliberately chose their own provider onto the
   organisation's account. It is a product decision: put it to the owner, and
   add a criterion that the row-10 notice says the personal provider is
   set aside (P2-AC-22 tests row 5 only).
3. **Product-level assumptions to confirm with the owner:** P2-D2 (above),
   P2-D4 (re-ask after a key rotation, against "ask once"), P2-D3 (an admin can
   cut every member's AI by forbidding personal keys before the setup is
   complete), and break-glass covering several changes (Blocking 4).
   P2-D1 and P2-D5 to P2-D11 are interface or engineering choices.
4. **Step-up landing consumer.** `SsoAdminStepupSection.tsx:40-57` reads
   `stepup` whatever the purpose. If an AI-purpose landing keeps
   `stepup=granted`, it would also say the grant "is used by your next SSO
   configuration change". P2-AC-32 should state that the SSO section shows no
   outcome for an AI-purpose landing.
5. **Destination refusals on the organisation's configuration misdirect the
   member.** The approval check runs after the row (186-187), and its sentences
   tell the user to fix their own Settings (`backend/nlp/destination.go:117-121`,
   `property: azureEndpoint`). The spec should say who is named as able to fix
   it in rows 4/9, and P2-AC-29 should cover a complete but unapproved
   organisation configuration.
6. **Code rollback (§7, 685-686) is not "safe" for O-12.** Part 1's code
   ignores the forbid setting, so personal keys are sent again while the stored
   setting says forbidden. Release notes must say so.
7. **MCP.** `mcp/client.go:22-90` sends no user credential to the backend, so
   P2-AC-33's "called for them" can only be tested against a fake backend. The
   "nothing sent" half rests on P2-AC-29. §3.4's "given by the person, in the
   web app" cannot be enforced by the server. Restate it as: no MCP tool and no
   server-side path records a confirmation.
8. **The admin is named as able to fix it when no admin can.** On a deployment
   with neither Google nor Microsoft step-up configured, and no break-glass
   list, rows 6, 8 and 11 still name the organisation's admin. Say whether that
   is intended.
9. **Stage order.** The branch already contains the contract
   (`contracts/features/NEW-llm-bring-your-own-part2.*`, `openapi.yaml`), a
   change to `backend/api/handlers_sso_stepup.go` and regenerated
   `paceday.gen.go`, while the spec says "not yet challenged". Re-check the
   contract against the revised spec.

### Criteria I could not falsify
- P2-AC-7: "any operation, in any status" names no reads (Blocking 3).
- P2-AC-33: the MCP server has no per-user identity on `main`, so "for a member
  in row 5" cannot be set up end to end (Non-blocking 7).
- P2-AC-2 (break-glass): the window is tested but the number of changes in it
  is not (Blocking 4).

### Citations I checked and found wrong
- §3.4 (273-274), "the owner's 're-ask if the provider changes'": not in the
  owner's decisions (2026-10-28) or the parent spec. The owner said "ask once".
- All other §2 citations match the code at ae5c2cf.

### Consumers this breaks (not named in §2.4)
- clockwise-like/e2e/tests/llm-bring-your-own.spec.ts:96-115: an
  organisation member asserts `enablers: ['user']` and "No provider chosen".
  P2-AC-14, P2-AC-31 and P2-AC-35 change both.
- clockwise-like/contracts/features/NEW-llm-bring-your-own.yaml:149: Part 1's
  AC-34.
- smart-calendar-flow/src/lib/aiProviders.test.ts:59-66: "No provider chosen"
  for `organization`, which changes for members (P2-AC-31).
- smart-calendar-flow/src/components/settings/SsoAdminStepupSection.tsx:40-57:
  reads `stepup` whatever the purpose (Non-blocking 4).

## Revision 1 — 2026-10-28 (answers challenge revision 0)

Author: spec-author. Every finding of the challenge above, with what was done.
"Applied" means the live text (§§2-9) now says it; the section or criterion
is named. Nothing in this revision was tested or built: it is text.

| Finding | Resolution | Where |
|---|---|---|
| B1 A withdrawn confirmation comes back when personal keys are forbidden | Applied. A settings write that stores "off" or one of the five providers deletes the member's stored confirmation. New criterion: confirmed follower → own provider (+ key) → admin forbids → next parse, brief refresh and probe answer row 10 and send nothing; also off/own provider → settings write back to "follow" → row 5. Under P2-D2's new default the forbidden case is row 10 anyway; the deletion makes it hold whatever the owner answers to OQ-P2-2. | §3.4 ("Choosing one's own provider, or 'off', ends the confirmation"), §5, §6, P2-D14, P2-AC-36 |
| B2 The confirmation is not tied to the configuration actually sent | Applied. (a) "Confirmed" now means the stored confirmation names exactly the version the request reads; configuration and confirmation are read as one consistent state; a request sends exactly the confirmed version's values, else row 5 (row 10 no longer sends under P2-D2). (b) The Settings choice confirms only the displayed version, through the confirmation operation and its stale-version rule; a settings write alone (including a whole-document write) records no confirmation. One criterion for each. | §3.1 (definition, "Row 4 sends only … of the confirmed version"), §3.4, P2-D14, P2-AC-37, P2-AC-38, P2-AC-22 |
| B3 The organisation key can leak through provider error text; P2-AC-7 not falsifiable | Applied. New §3.1a: the org key and users' keys never appear in any answer, stored brief text, log, Sentry report or operator record; provider/gateway error text has every occurrence of the org key and the caller's keys replaced before it is stored, returned, logged or reported. §2.5 cites today's paths. P2-AC-7 now names every read it covers; new P2-AC-39 uses an echoing gateway test double for (i) brief read after a failed refresh, (ii) parse and probe answers, (iii) logs, error reporter, operator records. §6 extends "write-only" to logs and error reports (stated as a widening). Exact-string only; transformed echoes are out of scope (§5). | §2.5, §3.1a, §5, §6, P2-AC-7, P2-AC-39 |
| B4 Break-glass is not "repeated per change" | Applied as the conservative reading: one organisation-AI change per fresh break-glass sign-in; a second change with the same session is "sign in again". Recorded as factory-assumed P2-D13, pending the owner (OQ-P2-4). PAC-50's SSO break-glass rule unchanged. Criterion for the second change being refused added to P2-AC-2. | §3.2, §6, §7 (c), §8, P2-D13, P2-AC-2 |
| NB1 Wrong attribution "the owner's 're-ask if the provider changes'" | Applied. The phrase is removed. §3.4, §8 and P2-D4 now say the owner said "tell and ask once" and did not say when to ask again; asking again on a key or destination change widens that and is the factory's choice, pending the owner. | §1, §3.4, §8 OQ-P2-1, §9 O-13 and P2-D4 |
| NB2 §8 claims every default is the more conservative choice; false for P2-D2 | Applied. P2-D2's default is changed to the conservative alternative (not moved; row 10 "personal keys are forbidden by your organisation; choose 'Use my organisation's AI' in Settings"; nothing sent until they choose it and confirm). Row 9 is reserved for the other answer. §8 no longer claims every default is conservative; it states each question neutrally and says each default is factory-assumed, pending the owner. The row-10 sentence says the personal provider and key are kept but not used (P2-AC-18). | §3.1 (table, consequences), §3.4, §3.6, §8, P2-D2, P2-AC-18, P2-AC-29 |
| NB3 Product-level assumptions to confirm with the owner | Applied. P2-D2, P2-D3, P2-D4 and P2-D13 are listed as product-level in §9 and put as OQ-P2-1 to OQ-P2-4 in §8 with their current defaults. OQ-P2-1, -2 and -4 match the questions the owner was asked on 2026-10-28 (the factory's owner-decisions record, "Still open"); P2-D3 was not among those and is added as OQ-P2-3, to be asked. | §8, §9 |
| NB4 Step-up landing consumer (SsoAdminStepupSection) | Applied. P2-AC-32: the SSO section shows no outcome for an AI-purpose landing, and the AI section none for an SSO landing. Consumer listed in §2.4. | §2.4, §3.6, P2-AC-32 |
| NB5 Destination refusals on the organisation's configuration misdirect the member | Applied. In row 4 a destination refusal names the organisation's AI destination and its administrator, never the member's own Settings. A separate criterion (P2-AC-41) covers a complete but no-longer-approved organisation configuration, rather than folding it into P2-AC-29, because the settings read does not check destinations (Part 1) and P2-AC-29 requires all four answers to agree. | §3.1, P2-AC-41 |
| NB6 Code rollback is not safe for O-12 | Applied. §7 now says a code rollback without the down migration re-enables personal keys in organisations that forbade them, and release notes must say so. | §7 |
| NB7 MCP: no per-user identity; "in the web app" cannot be enforced | Applied. P2-AC-33 is restated against a stand-in backend plus the tool list; "nothing sent" rests on P2-AC-29. §3.4 now says no MCP tool and no server-side path other than the confirmation operation records a confirmation, and that the server cannot tell a person from a program. | §3.4, §3.7, P2-AC-33 |
| NB8 The admin is named when no admin can be proven | Answered: intended, recorded as factory-assumed P2-D12 with the reason (operator configuration, not member state; one state, one answer). | §3.1, §8, P2-D12 |
| NB9 Stage order: contract already on the branch | Applied. The contract was re-checked against this revision and amended in the same push (contract revision 2, `contracts/features/NEW-llm-bring-your-own-part2.md`): P2-D2's default, the settings-write side effect, the admin's configurationVersion, the redaction rule, break-glass once per session, the destination refusal's configuration. | contract revision 2 |
| Could not falsify: P2-AC-7 | Fixed by naming the reads (P2-AC-7) and the echo case (P2-AC-39). | P2-AC-7, P2-AC-39 |
| Could not falsify: P2-AC-33 | Fixed by restating it against a stand-in backend (NB7). | P2-AC-33 |
| Could not falsify: P2-AC-2 break-glass count | Fixed (B4). | P2-AC-2 |
| Wrong citation §3.4 "re-ask if the provider changes" | Removed (NB1). | §3.4 |
| Consumers not named: e2e `llm-bring-your-own.spec.ts`, Part 1 AC-34, web `aiProviders.test.ts`, `SsoAdminStepupSection.tsx` | Applied: listed in §2.4 as amended (not deleted) when this part lands. The contract manifest lists them in consumersAffected and mergeConditions. | §2.4 |

Also changed in this revision, from the contract challenge (revision 1,
recorded in the contract rationale): an administrator's change names the
configuration version they were shown, and a stale one is refused as a
conflict before the proof is spent (contract challenge B-1; §3.2, P2-D15,
P2-AC-40).

## Challenge, revision 1 — 2026-10-28

**Verdict**: accept-with-changes

Checked by: `git fetch origin`; `git show` / `git grep` on `origin/factory/new-llm-byo-part2` (0c81d42), clockwise-like `origin/main` (ae5c2cf) and smart-calendar-flow `origin/main` (eea0cf6). No test, build or `make verify` was run. Spec line numbers are of 0c81d42.

### Revision 0 findings, re-checked
- B1 (withdrawn confirmation returns): fixed. §3.4 (402-408) deletes the confirmation when "off" or a provider is stored. P2-AC-36 tests it. P2-D2's new default makes the forbidden case row 10 anyway. Residual: non-blocking 4 below.
- B2 (confirmation not tied to sent values): fixed. "Confirmed" names the version read (199-203). Row 4 sends only that version (242-250). Only the confirmation operation records one (395-401, 410-415). P2-AC-37 and P2-AC-38 test both halves. "Read configuration and confirmation as one consistent state" can be built: the configuration's values and its version are one stored record, and the confirmation's version is compared against it. P2-AC-37 fails today, because row 4 does not exist and the answer is row 6. Wording issue: non-blocking 1.
- B3 (key leaks via error text): partly fixed. §3.1a, §2.5, P2-AC-7 and P2-AC-39 now name the paths and are testable. The residual is understated (Blocking 1).
- B4 (break-glass not per change): fixed as P2-D13 / OQ-P2-4, with the second-change case in P2-AC-2. Concurrency: non-blocking 5.
- NB1, NB2, NB4-NB9: fixed as the Revision 1 table says. NB3: fixed in the spec; OQ-P2-3 must still be put to the owner (§8, 950-953).

### Blocking

1. **The redaction residual is understated: a member can receive the whole organisation key in reversible form.**
   - §3.1a (280-283) describes what is not covered as "a transformed fragment of a key (say, its last four characters)".
   - §5 (882-883) adds "an encoding" to that exclusion. §6 (887-893) still says no response in any status carries an organisation key.
   - The brief is the member-visible path. A failed brief stores the provider or gateway body verbatim (`backend/engine/meeting_brief_service.go:87-90`, via `backend/nlp/parser.go:122-123`). The member reads it back through the brief read. Parse failures are a generic 500 and probe failures a fixed 502.
   - A gateway (owner O-10) may echo the key it was given in a reversible encoding: JSON with `/` escaped as `\/`, or percent-encoded. Exact-string replacement then misses the whole key, and it reaches the member, who reverses the encoding trivially.
   - This is not hypothetical for O-10 gateways, whose key format is the gateway's own, or for Bedrock, whose API keys are base64 and contain `+`, `/` and `=`.
   - *Resolves when* one of these holds:
     - (a) The spec requires that member-visible text produced by a request on the organisation's configuration (the stored brief text, every answer) carries no provider or gateway error text, so the encoding residual is limited to operator logs and error reports. P2-AC-39 gains a case where the double echoes the key JSON-escaped.
     - (b) §3.1a, §5 and §6 state exactly that a member can receive the full organisation key in a reversible encoding through the brief read, and the owner accepts that.

2. **A confirmation can match a configuration the member never saw, after a removal and a new setup.**
   - §3.2 (328-330): "Removing the provider removes the whole configuration, key included."
   - §7(a) (913-916) stores "one AI configuration per organisation" with its version.
   - §3.4 (372-379) ties a confirmation to a version number.
   - Nothing says the version is never reused within an organisation, and nothing ends confirmations when the configuration is removed. The contract implies monotonicity ("`0` when no configuration was ever set. Advances by one …", openapi.yaml 11021-11025), but the spec does not require it, and no criterion tests it.
   - Scenario: A at version 1 (OpenAI, K1); M confirms version 1. The admin removes the provider, and the stored configuration is deleted. Later the admin sets Anthropic with K2, starting again at version 1. M's next parse sends M's text and meeting details to the new account without the §3.4 notice.
   - *Resolves when* §3.4 states that a configuration version is never reused within an organisation, including across removal and a new setup (or that removing the configuration ends every confirmation for it). A criterion covers: confirm version V → remove → set a new configuration → the next parse answers row 5 and sends nothing.

### Non-blocking

1. **"Version" and the values it covers disagree.** The version does not advance on a model or personal-keys change (§3.4, 377-378; P2-AC-23, 703-704). Yet §3.1 (242-244) and P2-AC-37 (824-826) require sending "V's … model", and §3.2 (331-336) refuses a change when "the configuration has changed since", which a version check cannot detect for a model-only or personal-keys change. State that a version fixes provider, key and destination, that the model sent is the current one, and that the admin conflict is "the version has advanced".
2. **Redact the key that was sent, not the one stored now.** §3.1a (276-277) says "the organisation's key". After a key rotation during a request, redacting the newly stored key misses an echo of V's key. Say "the keys sent with this request", and add the rotation case to P2-AC-39.
3. **A forbid made during a request.** "While forbidden, no personal key is sent" (240-241) cannot hold for a request decided before the forbid was stored. Say "a request decided after the forbid is stored".
4. **Code rollback brings back revision 0's B1.** Part 1's code does not delete confirmations when "off" or a provider is stored. §7 (937-946) should state this, or require confirmations made before a code rollback to be invalid after it.
5. **Break-glass, once per sign-in, under concurrency and failure.** Two simultaneous changes with the same fresh session must not both succeed; P2-AC-2 (556-560) tests them only one after another. P2-AC-5 (577-579) says what a storage failure does to a grant, not to a break-glass session. The session has no identifier besides its issue time (`backend/auth/jwt.go:17-27`, one issuance point at `handlers_auth.go:214`); two sign-ins in the same second count as one session, which is conservative but should be stated.
6. **Probe precedence.** For a follower in a forbidding organisation, a probe naming nothing and carrying a key matches both §3.5 bullet 2 ("refused", 432-434) and P2-AC-20 ("personal keys are not allowed", 677-680). Say which answer wins.
7. **The destination is readable by every member.** §3.2 (296-300) shows the gateway URL to all members. The approval rule refuses userinfo but not a query string or a credential in the path (`backend/nlp/destination.go:124-135`). Say whether such a URL is refused for the organisation, or warn the admin that every member can read it.
8. **OQ-P2-3 has not been asked yet** (§8, 950-953). None of the four factory defaults is unsafe.

### Criteria I could not falsify
- None outright. P2-AC-37 should say the change is injected at each storage access the request makes.

### Citations I checked and found wrong
- `mcp/client.go:22-90` (§3.7, P2-AC-33): the file has 88 lines on ae5c2cf. Trivial.
- All other new citations match.

### Consumers this breaks
- None beyond those §2.4 now names.

## Revision 2 — 2026-10-28 (answers challenge revision 1)

Author: spec-author. Every finding of challenge revision 1 above, with what
was done. "Applied" means the live text (§§2-9) now says it. Blocking 1 and
2 were settled by the orchestrator as factory-level decisions (P2-D16,
P2-D17), not by the owner. Nothing in this revision was tested or built: it
is text. The contract was amended in the same push (contract revision 3).

| Finding | Resolution | Where |
|---|---|---|
| B1 Redaction residual understated: a member can receive the organisation key in a reversible encoding through the brief read | Applied, option (a) (orchestrator, factory-level, P2-D16). Member-visible text from a request on the organisation's configuration (every answer in every status, the stored brief text) carries no provider or gateway error text: a failure gives a fixed Paceday sentence ("The organisation's AI provider returned an error."). Provider text goes only to the server log and Sentry, redacted of the keys sent with the request and the caller's own keys. §3.1a, §5 and §6 now state the residual exactly: encoded or partial echoes can reach the server log and Sentry only, never a member, an answer, a stored brief or an operator record. P2-AC-39 gains the JSON-escaped and percent-encoded echo cases, asserting the brief and every answer carry no provider text at all (a marker string from the double's error body is absent). Own-key requests: Part 1 behaviour kept (provider text in the member's own failed brief, redacted of their own keys), because the only key there is their own and it is their only diagnosis; extending the rule is not trivial in effect, so it is not done, and §3.1a and §5 say so; P2-AC-39 (vi) pins it. | §3.1a, §3.8, §5, §6, P2-D16, P2-AC-39 |
| B2 A confirmation can match a configuration set after a removal | Applied (orchestrator, factory-level, P2-D17). A version is never reused within an organisation, including across removal and a new setup; the organisation's version count survives removal (§7 (a): removal empties the values, the count stays). New criterion P2-AC-42: confirm V → remove → set a new configuration → next parse row 5, nothing sent; also when the new configuration repeats the old provider and key. | §3.2, §3.4, §7, P2-D17, P2-AC-42 |
| NB1 "Version" and the values it covers disagree | Applied. A version fixes provider, key and destination; the model sent is the one read in the same consistent read; the personal-keys setting is not part of it. The admin conflict is "the version has advanced"; a change of only the model or personal-keys setting is not a conflict and the later change wins for those values. P2-AC-37 now says "V's provider, key and destination with the model read together with them", plus a model-only case. | §3.1 (definitions, row-4 consequence), §3.2, §3.4, P2-AC-37 |
| NB2 Redact the keys sent, not the one stored now | Applied. §3.1a redacts every key sent with this request (the old key after a rotation in flight) and the caller's own keys. P2-AC-39 (iv) adds the rotation case. | §3.1a, P2-AC-39 |
| NB3 A forbid made during a request | Applied. "While forbidden, no personal key is sent" now reads "by any request decided after the forbid is stored"; a request decided before may complete as decided. | §3.1 |
| NB4 Code rollback brings back revision 0's B1 | Applied. §7 states that Part 1's code does not delete confirmations, so a re-upgrade after any code rollback must delete every stored confirmation before Part 2 serves a request (every follower is asked again); release notes and the stage-4 api PR's rollback section state the step. | §7 |
| NB5 Break-glass once per sign-in under concurrency and failure; same-second sessions | Applied. Checking and recording the session's use are one indivisible step; two concurrent changes cannot both succeed (P2-AC-2 gains the concurrent case). A storage failure leaves the session unused (P2-AC-5, consistent with the contract's single transaction). Two sign-ins in the same second share an issue time and count as one session; the second is refused, which fails safe; stated in §3.2 and P2-D13. | §3.2, §7 (c), P2-D13, P2-AC-2, P2-AC-5 |
| NB6 Probe precedence for a key while forbidden | Applied. "Personal keys are not allowed" wins over every probe answer except a malformed request, including a confirmed follower's probe naming nothing (or "follow my organisation") with a key. P2-AC-20 says so. | §3.5, P2-AC-20 |
| NB7 The destination is readable by every member | Applied (orchestrator: refuse for the organisation). An organisation destination address with a query string, fragment, credentials before the host, or a path other than the operator-approved one (exact gateway prefix; no path for an Azure endpoint) is refused on save, before the proof is spent (P2-D18, P2-AC-4). The web also tells the admin every member can read the destination fields (§3.6, P2-AC-30). | §3.2, §3.6, §3.8, P2-D18, P2-AC-4, P2-AC-30 |
| NB8 OQ-P2-3 not asked yet | Done: the owner-decisions record now lists all four (OQ-P2-1 to OQ-P2-4) as asked on 2026-10-28; §8 says so. Factory defaults apply meanwhile. | §8 |
| Could not falsify: P2-AC-37 injection points | Applied: the test store applies the change at each storage access the request makes, one run per access. | P2-AC-37 |
| Citation `mcp/client.go:22-90` | Fixed to `mcp/client.go:13-88` (the file has 88 lines; the client type starts at 13). | §3.7, P2-AC-33 |

Also changed in this revision, from contract challenge revision 2 (recorded
in the contract rationale), as orchestrator decisions at factory level:

- B-1′: the version check and the storing of an administrator's change are
  one indivisible conditional step (covering the first setup), and spending
  the proof is part of it; a conflict never spends the proof. Consequence: a
  storage failure no longer spends the proof either (P2-AC-5 rewritten;
  revision 1 said it was spent). P2-AC-40 gains the interleaved case and the
  two-first-setups case. (§3.2, P2-D15, P2-AC-5, P2-AC-40.)
- Non-blocking 3: refusals decidable from the change alone come before the
  version check; refusals that depend on what is stored come after it.
  (§3.2, P2-AC-40.)
- Non-blocking 4: each kind of bad version gets one message, and a missing
  version is not read as version 0. (P2-AC-4.)

## Challenge, revision 2 — 2026-10-28

**Verdict**: accept

Checked by: `git fetch origin`; `git show` / `git diff 0c81d42 3ab38c7 -- docs/specs` on `origin/factory/new-llm-byo-part2` (3ab38c7); `git show` / `git grep` on clockwise-like `origin/main` (ae5c2cf); the branch contract (`contracts/features/NEW-llm-bring-your-own-part2.md`, `contracts/openapi/openapi.yaml`); the factory owner-decisions record (LLM org key, OQ-P2-1 to OQ-P2-4 listed as asked). No test, build or `make verify` was run. Spec line numbers are of 3ab38c7.

### Revision 1 findings, re-checked
- B1 (encoded echo reaches a member): fixed. §3.1a (287-311): every member-visible text from a row-4 request, including the stored brief, is a fixed Paceday sentence with no provider text, so no encoding of the organisation key can reach a member. Provider text goes only to the server log and Sentry, redacted of the keys sent and the caller's keys. The residual (encoded or partial echoes in the log and Sentry) is stated the same way in §3.1a, §5 (1042-1048) and §6 (1055-1067). P2-AC-39 (iii) tests the JSON-escaped and percent-encoded echoes. On ae5c2cf, parse and probe failures already answer with fixed text (`server_errors.go` `genericServerMessage`), so the brief (`meeting_brief_service.go:87-90`) is the only path the rule changes, as §3.1a says.
- B2 (version reuse after removal): fixed. §3.2 (387-388), §3.4 (459-465), §7 (a) (1090-1093), P2-D17 and P2-AC-42, which includes the case of re-setting the same provider and key. The contract agrees: removal advances `configurationVersion` and keeps the count.
- NB1 (version scope): fixed. 205-207, 249-260, 389-403, 466-475; P2-AC-37 has the model-only case.
- NB2 (redact keys sent): fixed. 300-303; P2-AC-39 (iv).
- NB3 (forbid during a request): fixed. 244-248.
- NB4 (code rollback restores revision 0's B1): fixed. §7 (1126-1136).
- NB5 (break-glass under concurrency and failure): fixed. 366-372, 412-416; P2-AC-2 concurrent case; P2-AC-5.
- NB6 (probe precedence): fixed. 523-531; P2-AC-20.
- NB7 (members can read the destination): fixed. 337-344; P2-AC-4; P2-AC-30.
- NB8 (OQ-P2-3 not asked): fixed. The owner-decisions record lists it.
- Citation `mcp/client.go:13-88`: correct.

### Blocking
None. I looked specifically for each of the following and found none:
- a path that sends a member's data to the organisation's account without a confirmation of the version sent;
- a personal key sent by a request decided after a forbid;
- the organisation key in any member-visible text or response;
- an organisation-AI change without a proof of that purpose;
- a grant or break-glass session spent across purposes or twice;
- a contradiction between sections that the text does not resolve.

### Non-blocking
1. **What a removal or a provider change does to "personal keys forbidden" is not stated.** §3.2 (330-335) makes the personal-keys setting part of "the configuration", and 385 says "Removing the provider removes the whole configuration". Read literally, a removal re-allows personal keys. The contract keeps the stored `personalKeysAllowed` on removal; for a provider change it says "every property not in the body takes its default", which is ambiguous for this flag. The spec should state that the personal-keys setting survives both a removal and a provider change, and a criterion (in P2-AC-42 or P2-AC-18) should pin it: forbid, then remove, then a member's own-key parse still answers row 11.
2. **§3.1a (290-291) covers a row-4 request that "fails at the provider or gateway".** The brief stores `LLM error: %v` for any `Complete` error (`meeting_brief_service.go:87-90`), including transport errors and timeouts. Keys are sent in headers (`llm_vertex.go:38`), so no key is exposed today. The rule should say "any failure of the call".
3. **P2-AC-4 (677-678) leaves "fragment" out** of the organisation destination refusals that §3.2 (339-340) requires.
4. **Row 10's sentence (213)** says "your own provider and key are kept" for a member stored on the removed provider (Ollama), who has no key. Word it so it holds for both cases.
5. **§3.1a (313-316) calls the own-key brief behaviour "Part 1's".** Part 1 stores that text unredacted (§2.5, 172-175). Redacting the member's own keys there is a small, harmless change, and §3.8 should name it as one.

### Criteria I could not falsify
- None.

### Citations I checked and found wrong
- None.

### Consumers this breaks
- None beyond those §2.4 names.
