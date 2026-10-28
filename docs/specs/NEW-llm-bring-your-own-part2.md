# Spec — NEW-llm-bring-your-own, Part 2: an organisation-wide AI provider and key

- **Linear**: PAC-73 (https://linear.app/paceday/issue/PAC-73). Part 2 of the
  spec `docs/specs/NEW-llm-bring-your-own.md` (the "parent spec").
- **Status**: draft, revision 1 (2026-10-28). Not yet challenged.
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
  nothing is sent before they confirm (O-13).

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

## 3. Desired behaviour

### 3.1 Who decides, and the one table

For each user, at each AI request (natural-language parse, meeting-brief
refresh, the provider probe) and at each settings read, exactly one row
applies. Rows are evaluated **in the order listed** (the numbers are the parent
spec's, kept so its tests keep their names; 9 to 11 are new and come right
after row 1). Only rows 2, 4 and 9 send anything.

"Complete" means the organisation has a provider, a key for it, and every
destination value that provider needs. "Forbids" means an administrator has
forbidden personal keys. "Confirmed" means the user has confirmed the
organisation's **current** configuration (§3.4).

| # | User's stored choice | Condition | Outcome and reason | Who can fix it |
|---|---|---|---|---|
| 1 | Off | none | not configured: off by the user's choice | the user |
| 9 | Own provider, or a removed provider | organisation forbids; complete; confirmed | **configured: the organisation's** | n/a |
| 10 | Own provider, or a removed provider | organisation forbids; complete; not confirmed | not configured: **personal keys are not allowed; confirm to use your organisation's AI** | the user, in one step |
| 11 | Own provider, or a removed provider | organisation forbids; not complete | not configured: **personal keys are not allowed and your organisation has not finished setting up AI** | the organisation's admin only |
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
- **Forbidding never rewrites the user's choice or key.** Rows 9 to 11 are
  decided from the organisation's setting at the moment of the request; the
  user's stored provider and keys are untouched. When the administrator allows
  personal keys again, the same user is back in row 2 or 3 with no action.
- **While forbidden, no personal key is sent**: not a stored one, not one typed
  into "Test connection" (§3.5).
- **Rows 9 and 4 send only the organisation's values**: provider, model, key
  and destination, and nothing from the member's account (parent D-18,
  unchanged).
- The row is still decided before any destination approval check (parent §3):
  only rows 2, 4 and 9 go on to it.
- A failed read of the caller's settings, organisation membership,
  organisation configuration or confirmation is a server error and nothing is
  written, fetched or sent (parent §3, extended to the two new reads).

### 3.2 The organisation's configuration

An organisation has at most one AI configuration: a provider (one of the five
key-based providers of Part 1), a model, the destination values that provider
needs (an optional approved gateway for OpenAI and Anthropic; endpoint,
deployment and API version for Azure OpenAI; region for Bedrock; none for
Vertex), one key bound to that provider, and whether personal keys are
allowed (allowed until an administrator says otherwise). The organisation's
destination values are subject to the operator's approval exactly as a user's
are (Part 1 §6).

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
(PAC-50 AC-14a). A step-up proof obtained for an SSO change cannot be spent on
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
  then get "nothing configured" naming the admin.
- A refusal of the change itself (malformed, a value that does not apply, an
  unapproved destination) happens before the proof is spent, so the admin can
  correct it without a new step-up. A storage failure after the proof is spent
  leaves the configuration unchanged and the proof spent.
- An administrator may forbid personal keys while the configuration is not
  complete. Members other than those who chose "off" then have no AI until it
  is complete (row 11, and row 6 naming only the admin). The web warns the
  admin before saving (§3.6).
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
(rows 4 and 9), the member is told, and asked to confirm, that their text and
meeting details (event titles, attendee emails, and the Slack and Notion
excerpts used for briefs) are sent to their organisation's AI provider account,
named, which its administrators can **access** outside Paceday. The wording
never says the administrators can "read" (or "see") the data. Until the member
confirms, nothing is sent (rows 5 and 10).

- The confirmation is stored per user, per organisation, and per **version** of
  the organisation's configuration. The version changes whenever the
  provider, the key or any destination value changes, because any of those may
  mean another provider account with other administrators; it does not change
  when only the model or the personal-keys setting changes. A member whose
  confirmation is for an older version is asked again (factory-assumed,
  widening the owner's "re-ask if the provider changes", §9).
- A confirmation names the version the member was shown. If the configuration
  changed in between, the confirmation is refused, nothing is recorded, and the
  member is shown the current configuration to confirm instead. A confirmation
  for an organisation with no complete configuration is refused.
- One member's confirmation never confirms another member.
- Choosing "Use my organisation's AI" in Settings shows the same notice and
  counts as confirming.
- The confirmation is given by the person, in the web app. No MCP tool gives
  it; an assistant acting through MCP gets the "confirm in Paceday" answer and
  sends nothing (§3.7).
- Brief refresh in rows 5, 10 and 11 is the not-configured answer of Part 1:
  nothing written, nothing fetched from Slack or Notion, nothing sent (parent
  D-12).

### 3.5 Test connection

- A probe that names a provider is always a test of the user's **own**
  configuration for that provider (parent D-19). This replaces the parent §3
  clause "or names exactly the organisation's provider and no model or key",
  which let a named provider select the organisation's key (factory-assumed,
  §9).
- A probe that names nothing, or names "follow my organisation", is decided
  by the table like a parse. In rows 4 and 9 it tests the organisation's
  configuration exactly as members' requests use it. A model or key in such a
  probe is refused (it would otherwise be silently ignored or, worse,
  combined with the organisation's key; parent D-18), and nothing is sent.
- While the organisation forbids personal keys, a probe that names a provider
  or carries a key is answered "personal keys are not allowed" naming the
  admin, and nothing is sent.
- An administrator who wants to test a key before saving it for the
  organisation does so with their own key (parent §5, unchanged).

### 3.6 What the web shows

- **Settings, organisation AI section**, for members only, identical for
  every member: provider, model, destination, "a key is set" or not,
  whether personal keys are allowed, and "an administrator of your
  organisation can change this". An "Edit" action explains the step-up (the
  scopes and what is lost without it, from PAC-50) before navigating, sends
  the admin through it for this purpose, and on return offers the form; a
  break-glass operator with a fresh session saves directly. A refused save
  says why and how to give the proof, and the section is unchanged. The key
  field is write-only. Forbidding personal keys while the configuration is not
  complete shows a warning first. A user with no organisation sees no such
  section.
- **Settings, the user's own choice.** Members are offered "Use my
  organisation's AI" (choosing it shows the §3.4 notice and confirms). A
  member stored as "follow my organisation" sees that choice selected and
  enabled, no longer the disabled "No provider chosen". A user with no
  organisation keeps Part 1's display (parent D-21). When the organisation
  forbids personal keys, the own-provider choices are shown disabled with the
  reason, and the user's stored choice and keys are shown as kept.
- **Dashboard bar and Settings notice.** In rows 5 and 10 the notice is the
  §3.4 text with a confirm action; after confirming, the request the user made
  is sent. In rows 6, 8 and 11 the notice names who can fix it as the table
  says (the admin appears only in an organisation, and alone when personal
  keys are forbidden).
- **Step-up landing.** An outcome for this purpose brings the admin back to
  the organisation AI section and states the outcome there; an SSO outcome
  lands exactly as today.

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

### 3.8 API surface and audit impact (what changes, in outline)

New: the organisation-AI configuration read (members), its change (proven
admins), and the member confirmation. Changed: the not-configured answer of
parse, brief refresh, probe and settings read (one new reason, the
organisation's admin as an enabler, rows 4 to 11 now reachable); the probe's
rules (§3.5); the step-up start, its options read and its landing (a purpose).
The register of hand-written versus generated endpoints gains three rows,
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
refused with "sign in again". [unit]

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
a key with no provider), then the answer names the problem, A's configuration
is unchanged, and the grant is still live. [unit]

P2-AC-5. Given a live grant and a change that is valid, when storing the
change fails, then the answer is a server error with no detail, A's
configuration is unchanged, and the grant is consumed. [unit]

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
change it; and no answer of any operation, in any status, contains A's key.
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

P2-AC-18. Given A complete and forbidding personal keys, and member M2 stored
on OpenAI with a key K2, when M2 parses, refreshes a brief or runs the probe
naming nothing, then before M2 confirms each answers row 10 naming only the
user and sends nothing, and after M2 confirms each uses A's configuration
(row 9). K2 is never sent. M2's stored provider and K2 are still stored; when
the admin allows personal keys again, M2's next parse carries K2 to OpenAI
with no action by M2. [unit]

P2-AC-19. Given A forbidding personal keys with no key set, when M2 of
P2-AC-18 parses, then the answer is row 11 naming only the organisation's
admin; when a follower parses, the answer is "nothing configured" naming only
the organisation's admin; nothing is sent in either case. [unit]

P2-AC-20. Given A forbidding personal keys, when a member's probe names
OpenAI with no key, names Anthropic with a key, or names nothing and carries a
key, then each answers "personal keys are not allowed" naming the
organisation's admin and no request reaches any host. [unit]

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
shows the same notice and counts as confirming. [unit] [e2e]

P2-AC-23. Given a member confirmed for A's current configuration, when an
admin changes A's provider, or its key, or any destination value, then the
member's next request answers row 5 (or 10) and sends nothing until they
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
every probe case) and the web notice for rows 5 and 10, when a test reads each
sentence, then the row 5 and row 10 sentences contain "access" and none of the
sentences contains the word "read", "reads" or "see" (as whole words,
case-insensitive). [unit] [e2e]

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

P2-AC-29. Given one account per row of §3.1 (all eleven), when each parses,
refreshes a brief, runs the probe naming nothing and reads its settings, then
all four give exactly that row's outcome, reason and who-can-fix-it, and only
rows 2, 4 and 9 send a request. [unit]

### 4.8 Web and MCP

P2-AC-30. Given A complete and two members (one an admin), when each opens
Settings, then the organisation AI section shows the same provider, model,
destination, "a key is set", personal-keys setting and "an administrator can
change this". When a member presses Edit, the page explains the step-up before
navigating and sends them through it for this purpose; on a granted return the
form is offered, and saving succeeds once; a member who cannot prove admin
status sees the refusal and who can change it, and the section is unchanged.
A break-glass operator with a fresh session saves without a step-up. The key
field never shows a key. Forbidding personal keys while A has no key shows a
warning before saving. A user with no organisation sees no section. [e2e]

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
outcome stated there; an SSO step-up lands and reads exactly as under PAC-50.
[e2e]

P2-AC-33. Given a member in row 5, 10 or 11, when the MCP tool
`clockwise_parse_command` is called for them, then it returns the
not-configured sentence and reason, nothing is sent to any provider, and the
MCP server offers no tool that confirms or reads the organisation's
configuration. [unit]

### 4.9 Contract

P2-AC-34. Given the assembled contract, when it is read, then: the
organisation configuration's read, change and confirmation are documented with
every refusal; the change's refusal distinguishes "no organisation",
"administrator step-up needed for this purpose" (with how to start it) and
"sign in again"; the step-up start, its options read and its landing document
the purpose; the not-configured reason set contains the personal-keys reason;
and no schema carries an organisation key in a readable response. [contract]

P2-AC-35. Given the e2e stack with no key anywhere and a fresh user in an
organisation with no configuration, when they parse, then the answer is
"nothing configured" naming the user and the organisation's admin, and the
dashboard says so. [e2e]

## 5. Explicitly out of scope

- **Withdrawing a confirmation** other than by choosing "off" or one's own
  provider (when allowed). A member who wants to stop sending to the
  organisation's account turns AI off; a separate "withdraw" is a later change.
- **Testing an unsaved organisation configuration** (parent §5).
- **Per-team keys, usage reporting, quotas** (parent §5).
- **An admin role stored in Paceday** (parent §5, D-6). The proof stays
  PAC-50's, per change.
- **Telling members when the organisation changes its configuration** other
  than by asking again at their next AI request (P2-AC-23). No email or
  notification is sent.
- **Confirming through MCP** (§3.7).

## 6. What must not change

- **Keys are write-only**, the organisation's included: no response, in any
  status, carries a user or organisation key (PAC-24; Part 1 §6;
  `TestLLMTest_BodyRulesAndNoKeyEcho`, `e2e/tests/settings.spec.ts`).
- **The probe body cannot carry a destination** (PAC-24 invariant).
- **Approved destinations** apply to the organisation's values exactly as to a
  user's (PAC-66 A and B, Part 1 §6).
- **PAC-50 is unchanged for SSO.** A step-up started without a purpose issues
  an SSO grant and lands exactly as today; the SSO write gate, its 403 shapes
  and its option read without a purpose are byte-identical. Protected by
  PAC-50's `TestPAC50_*` tests and AC-27 (a grant of another purpose is never
  spent by an SSO write).
- **Part 1 for users with no organisation** is unchanged: rows 1, 2, 3, 6 and
  8 with the user alone as the fixer.
- **"Off" is never overridden** (parent D-9).
- **Settings writes validate only what they change** (PAC-24 AC-17).

## 7. Migration and rollback

**Migration (up).** Adds storage for (a) one AI configuration per
organisation: provider, model, destination values, the key, whether personal
keys are allowed, the configuration version, and when and by whom it was last
changed; (b) one confirmation per user and organisation: the version confirmed
and when; (c) a new kind of operator record for organisation-AI changes. No
existing row is rewritten: settings rows, including every stored "follow my
organisation" choice, are untouched; PAC-50's grant table needs no change
(its purpose column takes the new value). Until an admin sets a
configuration, every organisation behaves as in Part 1 except that rows 6 and
8 in an organisation now also name its admin.

**Down.** Drops (a), (b) and (c), and deletes grants issued for the new
purpose (useless without this part) and operator records of the new kind
(required before the record kind can be removed). **This loses every
organisation's AI configuration including its key, every confirmation, and the
operator records of organisation-AI changes.** Release notes must say so.
After rollback, followers answer "nothing configured" (Part 1), members whose
organisation forbade personal keys are back on their own keys (their choice
and keys were never rewritten), and nobody is asked to confirm; after
re-upgrading, admins re-enter the configuration and members confirm again.

**Code rollback** without the down migration is safe: Part 1's code ignores
the new storage.

## 8. Open questions

None blocks the contract stage. Each below is a factory assumption recorded
in §9, and each is the more conservative choice (it sends less, or asks more).

- **OQ-P2-1** — Re-ask on a key or destination change, not only on a provider
  change (§3.4, P2-D4)? Owner: product owner. Cost of the assumption: members
  are asked again after a key rotation.
- **OQ-P2-2** — While personal keys are forbidden, should an own-provider
  member be routed to the organisation's configuration after confirming (rows
  9 and 10, P2-D2), or stay off until they choose "Use my organisation's AI"?
  Owner: product owner. The assumption follows O-12's "every member uses the
  organisation's key", gated by O-13's confirmation.

## 9. Decisions

### Owner

- **O-11 (owner, 2026-10-28, OQ-1: yes).** Setting or changing the
  organisation-wide AI provider and key requires PAC-50's admin proof
  (Google Workspace / Microsoft 365 admin step-up, or the operator's
  break-glass list), repeated per change. → §3.2, §3.3, P2-AC-1 to P2-AC-5,
  P2-AC-17.
- **O-12 (owner, 2026-10-28, OQ-7: yes).** An organisation admin may forbid
  personal keys, so every member uses the organisation's key. → rows 9 to 11,
  §3.5, P2-AC-17 to P2-AC-21.
- **O-13 (owner, 2026-10-28, OQ-8: yes).** Tell and ask once before the first
  AI request; the wording says the provider account's administrators can
  **access** the data, never "read"; nothing is sent before the user confirms.
  → §3.4, P2-AC-22 to P2-AC-27.
- Carried from the parent spec: O-1, O-5, O-6, O-7, O-8 (2026-10-27).

### Carried factory decisions (parent spec, pending owner confirmation)

D-6 (now confirmed by O-11), D-7, D-8, D-9, D-12, D-15 (confirmed by O-13),
D-18, D-19 (amended by P2-D6).

### Assumed by the factory, pending owner confirmation

- **P2-D1. The organisation-AI proof is a PAC-50 grant of its own purpose,
  obtained by a PAC-50 step-up started for that purpose.** Rejected: a second
  step-up flow (a second callback address to register with Google and
  Microsoft, and two copies of the checks); reusing the SSO grant (PAC-50
  AC-27 forbids it, and an SSO admin would silently gain AI authority).
- **P2-D2. While personal keys are forbidden, an own-provider or
  removed-provider member uses the organisation's configuration once they
  confirm (rows 9-10).** Their choice and keys are not rewritten. Rejected:
  leaving them off until they re-choose (contradicts "every member uses the
  organisation's key"); rewriting their choice (loses it when the ban is
  lifted). OQ-P2-2.
- **P2-D3. Forbidding is allowed while the configuration is incomplete**, with
  a warning in the web (row 11). Rejected: refusing it (the admin could still
  reach the same state by removing the key afterwards, so the refusal would
  protect nothing).
- **P2-D4. The confirmation is per user, per organisation, per configuration
  version; the version advances on a change of provider, key or any
  destination value.** Rejected: provider only (a new key or gateway can be
  another account with other administrators, which is exactly what O-13 tells
  the member about). OQ-P2-1.
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
