# Spec — PAC-87: organisation AI, apply the owner's answers OQ-P2-1 and OQ-P2-2

- **Linear**: https://linear.app/paceday/issue/PAC-87
- **Status**: draft
- **Author**: spec-author
- **Amends**: `docs/specs/NEW-llm-bring-your-own-part2.md` (the "Part 2
  spec", PAC-73 part 2, accepted at revision 2.1 and shipped in
  Enach/clockwise-like#240 and Enach/smart-calendar-flow#133). This file is
  the live text for everything it names; where it and the Part 2 spec
  disagree, this file wins. Every Part 2 clause not named here is unchanged.
  On acceptance, the Part 2 spec's §3.1 table, §3.4 "When the version
  changes" and the criteria listed in §4.0 below are replaced by this file's
  text (the Part 2 spec carries a pointer to this file).
- **Owner answers applied** (`.claude/factory/owner-decisions.md`, 2026-11-01):
  OQ-P2-1 (re-ask only when the provider changes), OQ-P2-2 (move members who
  chose their own provider onto the organisation's AI after they confirm
  once, keeping their own settings). Also re-checked: OQ-P2-3 (forbid before
  setup is complete, with a warning) and OQ-P2-4 (one change per fresh
  break-glass sign-in) — both answered "as shipped"; §2.6 shows they are,
  and §6 keeps them.
- **Inherited scope**: none.
  - No `x-uncertain` marker sits on an operation this changes:
    `grep -n x-uncertain contracts/openapi/paths/scheduling.yaml` finds none
    (the organisation-AI read, change and confirmation are at
    `scheduling.yaml:982` and `:1376`); the settings read/write, parse, brief
    refresh and probe carry none either (same check as the Part 2 spec,
    re-run on `32ecacf`).
  - `docs/factory/api-audit.md` has no finding on the organisation-AI
    operations (`grep -n "organization/ai\|OrganizationAI"` finds nothing).
  - PAC-84 (merged at `32ecacf`) seals the organisation's key and members'
    keys at rest. Nothing here changes that; §6 lists what must stay true.

## 1. Problem

An organisation administrator can set one AI provider and key for the whole
organisation, and can forbid members' personal keys. Members must be told,
and must confirm once, that their text and meeting details go to the
organisation's provider account, whose administrators can access them
(owner O-13). Part 2 shipped two factory defaults the owner has since
overruled:

1. **Members are asked again far too often.** Every key rotation, and every
   change to the gateway or Azure endpoint, invalidates every member's
   confirmation. Rotating a key is routine hygiene; today it silently turns
   AI off for the whole organisation until each member clicks "confirm"
   again. Administrators are punished for rotating keys, and members are
   trained to click through a consent notice that says nothing new. The
   owner's rule: ask again **only when the provider changes**.
2. **Forbidding personal keys turns AI off for exactly the members it is
   meant to bring in.** A member who had chosen their own provider is left
   with no AI until they find Settings and re-choose "Use my organisation's
   AI" — which also rewrites their own choice. The owner's rule: such members
   are **moved onto the organisation's AI after confirming once**, and their
   own settings are kept so that lifting the ban restores them.

## 2. Current behaviour (origin/main at `32ecacf`; web at smart-calendar-flow origin/main)

### 2.1 When a member is asked again (OQ-P2-1)

- One number, the configuration version, does two jobs. A change advances it
  when it alters the provider (including removal), writes any key (keys are
  never compared, so re-entering the same key counts), or alters any
  destination value (`backend/storage/org_ai.go:226-229`, the rule;
  `org_ai.go:260-290`, the code; `if providerChanged || keyWritten ||
  destinationChanged` at `:288`).
- Job 1, administrators' optimistic concurrency: a change names the version
  it was made from and is refused as a conflict otherwise
  (`org_ai.go:429-431`; contract `contracts/openapi/paths/scheduling.yaml:1103`,
  `:1152`).
- Job 2, consent: a confirmation stores the version
  (`storage/migrations/031_llm_org_ai.up.sql:37-43`, `PRIMARY KEY (user_id,
  org_id)`), and a member counts as confirmed only when the stored number
  equals the current version (`org_ai.go:103-107`, `Confirmed()`). So a key
  rotation or destination change puts every follower back in row 5
  (`backend/nlp/llm_factory.go:255-265`). The contract says so in words
  (`scheduling.yaml:1180-1191`, "re-entering the same key re-asks members").
- The confirmation operation refuses any version other than the current one
  (`org_ai.go:520-535`; `backend/api/handlers_org_ai.go:539-559`), so a key
  rotation between the member opening the notice and pressing "confirm"
  refuses the confirmation.
- Row 4 sends exactly the configuration it read in one SQL statement
  together with the confirmation (`org_ai.go:154-177`, `ReadOrgAIState`;
  `nlp/ai_source.go:36-49`), so a request never mixes two versions; the test
  is `TestPAC73P2_OneConsistentVersionPerRequest`
  (`backend/api/pac73p2_rows_test.go:585`).
- Tests pinning the shipped rule: `TestPAC73P2_ConfirmationPerVersion`
  (`pac73p2_rows_test.go:351`), `TestOrgAIStateAndConfirmation`
  (`backend/storage/org_ai_test.go:301`), `TestWriteOrgAIConfig_ProofVersionAndRecord`
  (`org_ai_test.go:105`).

### 2.2 Members on their own provider when personal keys are forbidden (OQ-P2-2)

- The decision table (`nlp/llm_factory.go:15-31`): row 10 (own or removed
  provider; forbids; complete) answers `personal_keys_forbidden` naming the
  user; row 11 (same, not complete) names only the admin. Row 9 is
  "reserved" and never answered (`llm_factory.go:29-31`). The code is
  `llm_factory.go:231-235`: forbidding is decided before the stored choice is
  looked at, and nothing in that branch reads the confirmation.
- Row 10's sentence tells the member to choose "Use my organisation's AI" in
  Settings (`llm_factory.go:396-400`).
- A member stored on their own provider *can* hold a confirmation (the
  confirmation operation does not look at the stored choice,
  `handlers_org_ai.go:539-559`), but it changes nothing: P2-AC-18 pins that
  "M2 confirms … the next parse still answers row 10"
  (`TestPAC73P2_PersonalKeysForbidden`, `pac73p2_rows_test.go:291`).
- Web: row 10's notice offers "Use my organisation's AI"
  (`smart-calendar-flow src/lib/aiProviders.ts:120-125`,
  `src/components/AINotConfiguredNotice.tsx:54-63`), which opens the consent
  dialog with `choose` set (`AINotConfiguredNotice.tsx:72`); on confirm the
  dialog confirms and then **rewrites the member's stored choice** to
  "organization" (`src/hooks/useOrganizationAI.ts:33-36`,
  `src/api/client.ts:330-332`). So today the only way out of row 10 discards
  the member's own choice, which is what the owner says must be kept.
- Settings shows, for a forbidden member on their own provider, that their
  choice and key are kept but not used (`src/pages/Settings.tsx:618-623`);
  the admin view labels the setting "Not allowed: members use the
  organisation's AI" (`src/components/settings/OrganizationAISection.tsx:74`),
  which is not what happens today.

### 2.3 How a confirmation ends

- A settings write that stores "off" or any of the five providers deletes
  the caller's confirmation **whether or not the stored choice changes**
  (`backend/api/handlers_settings.go:213-223`, `:240-250`;
  `org_ai.go:550-556`). The web normally sends only changed keys
  (`src/api/client.ts:864-875`, `settingsPatchBody`), but sends the whole
  document when it has no previous copy; the contract's settings description
  says the same ("Writing `""` or a provider deletes the caller's
  organisation confirmation").
- Under OQ-P2-2 this rule would silently undo a moved member's confirmation
  every time a whole settings document carrying their (unchanged) own
  provider is saved. §3.4 decides it.

### 2.4 Redaction and what row 4 sends

- A row-4 decision builds the client from the organisation's values only
  (`llm_factory.go:302-321`, `newOrgClient`) and adds the organisation's key
  to the caller's own keys in the redaction set (`llm_factory.go:226`,
  `:255-262`). Provider error text is never shown to a member for an
  organisation request (Part 2 §3.1a; `TestPAC73P2_NoKeyLeavesThroughProviderErrorText`,
  `pac73p2_rows_test.go:712`).
- The probe fills in the member's stored model when the probed choice is
  their stored choice (`llm_factory.go:202-205`). For a member stored on
  their own provider, that choice is a provider; today that path ends in row
  10 before the model matters. Under OQ-P2-2 it would reach the
  organisation's configuration — §3.3 forbids carrying the member's model.

### 2.5 Audit

- Every organisation-AI change writes one operator record in the same
  transaction: who, how (step-up or break-glass), the names of changed
  values, the new version, never a value or key (`org_ai.go:455-470`).
- A confirmation writes **no** operator record (`org_ai.go:520-548`); the
  stored row with its time is the only trace. Unchanged here (§5).

### 2.6 The two answers that match what shipped

- **OQ-P2-3**: an admin may forbid personal keys before setup is complete; the
  web warns first: "Your organisation's AI is not set up completely. If you
  turn personal keys off now, every member except those who turned AI off
  will have no AI until it is set up."
  (`OrganizationAISection.tsx:174`, `:281-285`; test
  `OrganizationAISection.test.tsx:180-182`). Backend accepts it (row 11 and
  row 6 admin-only, `llm_factory.go:235`, `:265-266`).
- **OQ-P2-4**: one change per fresh break-glass sign-in, enforced atomically
  by a partial unique index on the operator record (`org_ai.go:390-413`);
  tests `TestPAC73P2_OrgAIBreakGlassOneChangePerSignIn`
  (`backend/api/pac73p2_org_ai_test.go:226`) and
  `TestWriteOrgAIConfig_BreakGlassOncePerSession` (`org_ai_test.go:192`); web
  message `OrganizationAISection.tsx:202`.

## 3. Desired behaviour

### 3.1 Two different questions, two different answers

"Has the configuration changed since this administrator looked at it?" and
"has this member agreed to where their data goes?" stop being the same
question.

- **Administrators' conflict check is unchanged.** Any change of provider,
  key or destination still makes an administrator's form that was shown the
  older state a conflict (Part 2 §3.2, P2-AC-40). A key rotation therefore
  still conflicts with another administrator's concurrent edit. Numbers used
  for this are still never reused.
- **A member's confirmation is a confirmation of the organisation's
  provider.** It stays valid across any number of key rotations and
  destination changes (gateway, Azure endpoint, deployment, API version,
  Bedrock region), and across model and personal-keys changes. It ends when
  the provider changes. Factory-assumed, **P87-D1**: removing the
  configuration counts as a provider change (the provider becomes "none"),
  so after a removal every member is asked again even if the same provider
  is set up afterwards; likewise P → Q → P asks again. Every provider change
  starts a new consent period that no earlier confirmation can match, ever
  (the "never reused" rule of Part 2 P2-D17 applies to consent periods too).
- **Accepted consequence (owner's explicit answer, recorded so nobody
  re-litigates it):** a new key or Azure endpoint may belong to a different
  provider account with different administrators. The consent notice names
  the provider, not the account, and the owner has decided that such a change
  does not re-ask. Nothing in this spec warns members of it; the admin view
  says it (§3.6).
- **What a confirmed request sends.** A request decided on the
  organisation's configuration sends the provider, key, destination and model
  of one consistent read of the configuration, and only if that read's
  provider is the one the member's confirmation covers (no provider change
  since the confirmation). It never combines values of two reads. A request
  racing with a key rotation may send either the old key or the new one,
  each with its own destination; a request racing with a provider change
  sends the old configuration consistently or answers "confirm" and sends
  nothing. (Replaces the Part 2 rule "sends only the confirmed version".)
- **Confirming.** A member confirms what they were shown. The confirmation is
  refused (nothing recorded, the current configuration returned to show
  instead) only when the provider has changed since it was shown, or the
  configuration is not complete. A key or destination change between showing
  and confirming does not refuse it: the member agreed to the provider, which
  is what the notice showed. Confirming twice is harmless.

### 3.2 The decision table (replaces Part 2 §3.1's table)

Rows evaluated in the order listed; only rows 2, 4 and 9 send anything.
"Confirmed" now means: the member's stored confirmation covers the
organisation's current provider (§3.1). Everything else is as Part 2 §3.1.

| # | User's stored choice | Condition | Outcome and reason | Who can fix it |
|---|---|---|---|---|
| 1 | Off | none | not configured: off by the user's choice | the user |
| 9 | Own provider, or a removed provider | organisation forbids; complete; confirmed | **configured: the organisation's** | n/a |
| 10 | Own provider, or a removed provider | organisation forbids; complete; not confirmed | not configured: **your organisation does not allow personal keys and provides AI through ‹provider›; the consent sentence (§3.5); your own choice and any key you saved are kept and used again if your organisation allows personal keys; confirm in Paceday to use your organisation's AI** | the user, in one step (confirming; the stored choice is not changed) |
| 11 | Own provider, or a removed provider | organisation forbids; not complete | unchanged | the organisation's admin only |
| 2 | Own provider | a key bound to it | configured: the user's own | n/a |
| 3 | Own provider | no key bound to it | not configured: that provider needs a key | the user |
| 4 | Follow the organisation | complete; confirmed | configured: the organisation's | n/a |
| 5 | Follow the organisation | complete; not confirmed | not configured: your organisation provides AI through ‹provider›; the consent sentence; confirm to use it (wording unchanged; "confirmed" per §3.1) | the user, in one step |
| 6–8 | unchanged | unchanged | unchanged | unchanged |

Consequences, stated so nobody has to derive them:

- **Off still wins.** A member who chose "off" is never moved, whatever the
  organisation forbids, and whatever confirmation they may hold.
- **Forbidding moves nobody without their confirmation.** Forbidding sends
  nothing on anyone's behalf: a member on their own provider gets row 10 and
  nothing is sent until they confirm (owner O-13, "nothing is sent before the
  user confirms", and OQ-P2-2, "after confirming once" — the two agree; no
  conflict). Confirming in row 10 is the same confirmation as in row 5, with
  the same notice plus one sentence about their kept settings.
- **Moving never rewrites the member's choice or keys.** In row 9 the
  member's stored choice, model, destination values and keys are untouched
  and none of them is sent or used. When personal keys are allowed again,
  the member is back in row 2 or 3 with no action.
- **A confirmation survives lifting the ban** (factory-assumed, **P87-D2**).
  If the organisation forbids personal keys again later and the provider has
  not changed meanwhile, the member is back in row 9 without being asked
  again: they agreed to that provider, and OQ-P2-1 says only a provider change
  re-asks.
- **A provider change re-asks moved members too**: a member in row 9 goes to
  row 10 until they confirm the new provider.
- **Removed provider (Ollama).** A member stored on a removed provider is
  treated as "own provider" in rows 9–11, as today in rows 10–11; their stored
  value is not rewritten.
- Row 9 is decided before the destination approval check exactly as row 4,
  and a destination refusal in row 9 is about the organisation's
  configuration and names its administrator, as in row 4 (Part 2 §3.1).

### 3.3 Requests in row 9

A request in row 9 is, in every respect, a row-4 request: it sends only the
organisation's values (parent D-18), never the member's key, model, gateway or
destination; provider error text is replaced by the fixed sentence of Part 2
§3.1a; logs and error reports are redacted of the organisation's key **and**
every key the member has stored (they are still stored, so they are still
secrets to redact). A probe that names nothing, for a member in row 9, tests
the organisation's configuration as the member's requests use it and does not
carry the member's stored model; a probe that names a provider or carries a
key is still answered "personal keys are not allowed" (Part 2 §3.5,
unchanged). Brief refresh in row 10 changes nothing and fetches nothing (Part
2 P2-AC-27, extended to the new row 10).

### 3.4 When a confirmation ends (replaces Part 2 §3.4 "Choosing one's own provider, or off, ends the confirmation")

- A settings write that **changes** the member's stored choice to "off" or to
  a provider different from the one stored ends the member's confirmation,
  as today. This keeps withdrawal working: a member in row 4 or 9 who chooses
  "off" is in row 1; a follower who chooses their own provider and is later
  forbidden is in row 10 and asked again (P2-AC-36's first case).
- A settings write that stores the **same** provider (or "off") the member
  already has — a whole settings document sent back unchanged, or saving a
  key or model for their own provider — no longer ends it (factory-assumed,
  **P87-D3**). Otherwise a member moved under OQ-P2-2 would be silently moved
  back to row 10 by any save of an unrelated setting, and asked again; that
  would break "ask once". It cannot widen consent: the only way to record a
  confirmation is still the member's own confirmation (Part 2 §3.4, "No path
  but the confirmation operation records one").
- Storing "follow my organisation" still records nothing.

### 3.5 Telling the member (wording)

The consent sentence and the wording rule are unchanged: the organisation's
provider account's administrators can **access** the data; no member-facing
sentence says "read", "reads" or "see". Row 10's sentence and web notice now
(a) name the organisation's provider, (b) contain the consent sentence, (c) say
the member's own choice and saved key are kept and will be used again if the
organisation allows personal keys, and (d) offer to confirm. Suggested web text
(wording not contractual beyond the rule):

> Your organisation does not allow personal API keys. It provides AI through
> ‹Provider›. Your text and meeting details (event titles, attendee emails,
> and the Slack and Notion excerpts used for meeting briefs) are sent to your
> organisation's AI provider account (‹Provider›), which its administrators
> can access outside Paceday. Your own choice (‹Own provider›) and any key you
> saved are kept, and are used again if your organisation allows personal
> keys. Nothing is sent until you confirm.

### 3.6 What the web shows

- **Row 10 notice** (dashboard bar and Settings): the §3.5 text with a
  "Review and confirm" action that confirms **without changing the member's
  stored choice**. The "Use my organisation's AI" action that rewrites the
  choice is no longer offered from row 10 (it stays for row 7, a removed
  provider in an organisation that allows personal keys, and in the Settings
  choice list). After confirming, the request the member made is sent.
- **Consent dialog opened from row 10**: same dialog, with one added sentence
  that the member's own choice and key are kept and used again if personal
  keys are allowed.
- **Settings, a member in row 9**: the own-provider choice is shown as today
  (disabled, "kept but not used"), plus a line saying AI runs on the
  organisation's AI (‹Provider›). A member in row 10 sees the notice above.
- **Settings, organisation AI section (administrators' form)**, before saving:
  - a change of provider, or removing the configuration: "Every member who
    uses your organisation's AI will be asked to confirm again before
    anything is sent to the new provider.";
  - a change of only the key and/or destination values: "Members are not
    asked to confirm again: they agreed to ‹Provider›, and this change keeps
    it.";
  - forbidding personal keys while the configuration is complete: "Members
    who use their own key will be asked once to confirm, then use your
    organisation's AI. Their own settings are kept and used again if you
    allow personal keys.";
  - forbidding while incomplete: the existing OQ-P2-3 warning, unchanged.
  None of these sentences contains "read", "reads" or "see". The existing
  destination hint "Every member of your organisation can read this value"
  (`OrganizationAISection.tsx:233-252`) is about members reading Paceday's
  configuration, not about provider administrators, and is not member-facing
  consent text; it may stay (the P2-AC-26 test scope is member-facing
  sentences).
- The admin view's "Not allowed: members use the organisation's AI" becomes
  true; it may add "after confirming once".

### 3.7 Audit

- Every organisation-AI change's operator record additionally says whether
  it starts a new consent period, i.e. whether members will be asked again
  (true exactly for a provider change, including removal and first setup).
  Never a value or key (Part 2 P2-AC-6 unchanged).
- Confirmations and moves still write no operator record (§5).

### 3.8 Existing data at deploy

- Every organisation's current configuration starts its current consent
  period at deploy such that **every member who has confirmed the current
  configuration stays confirmed** (no follower is asked again because of
  this deploy).
- A confirmation of an **older** configuration is not revived, even if only
  the key or destination changed since (the history needed to prove the
  provider did not change is not reliably stored; factory-assumed,
  **P87-D4**). Those members are asked once more — which the shipped
  behaviour would have done anyway.
- Confirmations held by members whose stored choice is not "follow my
  organisation" are ended at deploy (factory-assumed, **P87-D5**). Today such
  a confirmation could only come from a direct call to the confirmation
  operation and had no effect (§2.2); under OQ-P2-2 it would silently move the
  member onto the organisation's AI without them ever being shown the row-10
  text. Ending them means every moved member sees and confirms that text.
- No member's stored choice, keys or destination values are touched.
- After deploy, a consent period never matches a confirmation number issued
  before it unless that confirmation was of the current configuration.

### 3.9 MCP

Unchanged mechanism: `clockwise_parse_command` in row 9 is sent on the
organisation's configuration; in row 10 it returns the not-configured
sentence (which now says "confirm in Paceday") and sends nothing. No MCP tool
can confirm.

## 4. Acceptance criteria

### 4.0 Mapping

| Part 2 criterion | Replaced or amended by |
|---|---|
| P2-AC-18 | PAC87-AC-8, -9, -11 |
| P2-AC-22 | PAC87-AC-15 (row 5 unchanged; row 10 added) |
| P2-AC-23 | PAC87-AC-1 to -4 |
| P2-AC-24 | PAC87-AC-5 |
| P2-AC-26 | PAC87-AC-15 (row 10 notice now asks for confirmation, so it must contain "access") |
| P2-AC-29 | PAC87-AC-20 (row 9 now reachable) |
| P2-AC-36 | PAC87-AC-12, -13 |
| P2-AC-37 | PAC87-AC-6 |
| Part 2 table rows 5, 9, 10 | §3.2 |

### 4.1 Re-asking only on a provider change (OQ-P2-1)

PAC87-AC-1. Given organisation A complete on provider P with key K1, and a
follower M who confirmed A, when an administrator replaces the key with K2
(including re-entering K1), then M's next parse is sent to P with K2 and M is
not asked; M's settings read reports the organisation's configuration as
configured. Today it answers row 5. [unit]

PAC87-AC-2. Given the same M, when an administrator changes only a
destination value — for OpenAI or Anthropic the gateway (set, change,
remove); for Azure OpenAI the endpoint, deployment or API version; for
Bedrock the region — then M's next parse is sent with the new destination and
M is not asked. [unit]

PAC87-AC-3. Given the same M, when an administrator changes the provider P →
Q (with a key for Q), then M's next parse, brief refresh and probe naming
nothing each answer row 5 naming Q and send nothing; after M confirms, the
next parse is sent to Q. The same holds for P → Q → P (asked again after the
second change even though the provider is P again). [unit]

PAC87-AC-4. Given the same M, when an administrator removes the
configuration and then sets it up again with provider P (P87-D1), then M's
next parse answers row 5 and sends nothing. When an administrator changes
only the model or only the personal-keys setting, M's next request is sent
without asking (as today). [unit]

PAC87-AC-5. Given M was shown A with provider P, when an administrator
rotates A's key (or changes a destination) and M then confirms what they
were shown, then the confirmation is accepted and M's next parse is sent with
the current key. When instead the administrator changed the provider before
M confirmed, then the confirmation is refused, nothing is recorded, the answer
carries A's current configuration (naming the new provider), and M's next
parse answers row 5. Given A not complete, a confirmation is refused and
nothing is recorded (unchanged). [contract] [unit]

PAC87-AC-6. Given M confirmed A on provider P at configuration state S1, and
an administrator change to S2 stored after M's request began to be decided (a
test store applying it at each storage access the request makes, one run per
access), then: if S2 only rotates the key and changes the destination, every
run sends either all of S1's provider/key/destination/model or all of S2's,
never a mix; if S2 changes the provider, every run either sends all of S1's
values or answers row 5 and sends nothing, and never sends any value of S2.
[unit]

PAC87-AC-7. Given two administrators shown A in the same state, when one
rotates the key and the other then saves a change made from the state they
were shown, then the second save is refused as a conflict, carries A's
current configuration, and does not spend the second administrator's proof
(administrators' conflict check unchanged, P2-AC-40). The operator record of
the key rotation says members are not asked again; that of a provider change
says they are; neither contains a key. [unit]

### 4.2 Moving members onto the organisation's AI (OQ-P2-2)

PAC87-AC-8. Given A complete and forbidding personal keys, and member M2
stored on OpenAI with key K2, model m2 and a gateway g2, not confirmed, when
M2 parses, refreshes a brief or probes naming nothing, then each answers row
10 naming only the user; the sentence names A's provider, contains "access",
says M2's own choice and key are kept and used again if personal keys are
allowed, and says to confirm; nothing is sent. When M2 confirms (without any
settings write), then M2's next parse is sent with A's provider, key,
destination and model, and the outgoing request contains neither K2, m2 nor
g2; M2's settings read still reports OpenAI as the stored choice with a key on
file, and its AI status reports the organisation's configuration as
configured (row 9). Today the parse after confirming answers row 10. [unit]

PAC87-AC-9. Given M2 in row 9, when an administrator allows personal keys,
then M2's next parse carries K2 to OpenAI (via g2, with m2) with no action by
M2. When the administrator then forbids personal keys again without changing
the provider (P87-D2), M2's next parse is sent on A's configuration without
asking. When instead the provider changed meanwhile, M2's next parse answers
row 10 and sends nothing. [unit]

PAC87-AC-10. Given A complete and forbidding, and member M3 stored on the
removed provider (Ollama), when M3 parses, then the answer is row 10 (naming
Ollama as M3's kept choice); after M3 confirms, row 9; M3's stored value is
still Ollama. [unit]

PAC87-AC-11. Given M2 in row 9 and a provider double that answers with an
error body containing a marker X and the key it was sent, when M2's parse,
brief refresh and probe fail, then no answer and no stored brief contains X,
A's key or K2, and no server log line, error-reporter event or operator record
contains A's key or K2. [unit]

PAC87-AC-12. Given a follower M confirmed for A (personal keys allowed),
when M chooses OpenAI and saves a key, then M's confirmation is ended; when an
administrator then forbids personal keys, M's next parse answers row 10 and
sends nothing until M confirms again. Given M in row 9 chooses "off", then M
answers row 1 and nothing is sent; when M later stores OpenAI again, M answers
row 10 (asked again). Given A forbidding and member M4 stored "off" holding a
confirmation of A, M4 answers row 1 and nothing is sent. [unit]

PAC87-AC-13. (P87-D3.) Given M2 in row 9, when a settings write stores
OpenAI again unchanged (a whole settings document sent back, or only a new
key or model for OpenAI), then M2 is still in row 9 and the next parse is sent
on A's configuration. When a settings write changes M2's stored choice to
Anthropic, M2's confirmation is ended and the next parse answers row 10.
[unit]

PAC87-AC-14. Given M2 in row 9, when M2 probes naming OpenAI, or naming
nothing with a key, or naming nothing with a model, then the answer is
"personal keys are not allowed" naming the admin and nothing is sent; naming
nothing and carrying neither, the probe tests A's configuration without M2's
model. [unit]

### 4.3 Wording and web

PAC87-AC-15. Given every not-configured sentence the server can give
(including row 10's new sentence), the row 10 web notice, and the consent
dialog opened from row 10, when a test reads each, then row 10's server
sentence, the row 10 notice and the dialog contain "access", and none of the
member-facing sentences contains "read", "reads" or "see" (whole words,
case-insensitive). The admin form's four pre-save sentences of §3.6 contain
none of those words either. [unit] [e2e]

PAC87-AC-16. Given a member stored on OpenAI in A, A complete and forbidding,
when they use the natural-language bar, then the page shows the row 10
notice with a "Review and confirm" action and no action that changes their AI
choice; after confirming, the request is sent on A's configuration, and
Settings shows their own choice still OpenAI (kept, not used) and a line that
AI runs on the organisation's AI naming A's provider. No settings write
carrying "follow my organisation" is sent by the web during this flow. [e2e]

PAC87-AC-17. Given an administrator editing A's configuration in the web,
when the form changes the provider (or removes it), then before saving the
form shows that every member will be asked to confirm again; when it changes
only the key and/or destination values, it shows that members are not asked
again; when it forbids personal keys with A complete, it shows that members
on their own key will be asked once and keep their settings; when it forbids
with A not complete, it shows the existing OQ-P2-3 warning. [unit]

### 4.4 Data at deploy, rollback, one answer per state

PAC87-AC-18. Given, before the migration, A at configuration version 7 on
provider P with personal keys forbidden; follower X confirmed 7; follower Y
confirmed 5; member Z stored on OpenAI holding a confirmation of 7; then after
the migration: X's next parse is sent on A without asking; Y's answers row 5;
Z's answers row 10 (P87-D5). After a later key rotation of A, X's next parse
is still sent without asking. After a later provider change, neither X's
stored confirmation nor any confirmation issued before the migration
matches. No member's stored choice or key differs before and after. [unit]

PAC87-AC-19. Given the migration is reverted, when any follower or row-9
member next parses, then they answer row 5 or row 10 and nothing is sent
until they confirm again; no confirmation from before the revert is read as
covering a configuration it did not confirm. [unit]

PAC87-AC-20. Given one account per row of §3.2 (rows 1 to 11, row 9 now
included), when each parses, refreshes a brief, runs the probe naming nothing
and reads its settings, then all four give that row's outcome, reason and
who-can-fix-it, and only rows 2, 4 and 9 send a request. [unit]

PAC87-AC-21. Given a stand-in backend whose parse answer is row 10's
not-configured answer, when the MCP tool `clockwise_parse_command` is called,
then it returns that sentence and reason unchanged; no MCP tool confirms.
[contract]

## 5. Explicitly out of scope

- **Recording confirmations as operator events.** Not asked for; the stored
  confirmation with its time is the record, as shipped. A consent-audit trail
  would be a separate ticket.
- **Notifying members (email, banner) when they are moved or when the
  provider changes.** They are told at their next AI request or in Settings,
  as Part 2 does.
- **Re-asking on account change behind the same provider** (§3.1 accepted
  consequence) — the owner's answer.
- **Reviving older confirmations** whose provider provably did not change
  (P87-D4).
- **Changing OQ-P2-3 or OQ-P2-4 behaviour** — answered "as shipped".
- **PAC-84 encryption at rest** — unchanged; no new secret is stored.

## 6. What must not change

- Administrators' conflict check on every provider, key or destination change,
  and its proof-not-spent rule: P2-AC-40, `TestPAC73P2_OrgAIStaleVersionIsAConflict`
  (`pac73p2_org_ai_test.go:391`), `TestWriteOrgAIConfig_FirstSetupConditionIsTheVersion`
  (`org_ai_test.go:277`).
- One change per fresh break-glass sign-in (OQ-P2-4):
  `TestPAC73P2_OrgAIBreakGlassOneChangePerSignIn`, `TestWriteOrgAIConfig_BreakGlassOncePerSession`.
- Forbidding before setup is complete is allowed and warned (OQ-P2-3): the web
  test `OrganizationAISection.test.tsx:180-182`; the backend accepts it — **no
  backend test names this case explicitly**; P2-AC-19 covers its row 11 effect
  (`TestPAC73P2_PersonalKeysForbidden`). A test that a forbid with no key set
  is stored should be added (finding).
- "Off wins"; no personal key sent while forbidden (P2-AC-20, -21);
  "nothing sent before the user confirms"; no MCP or server path records a
  confirmation; one member's confirmation never confirms another (P2-AC-25).
- Row 4/9 sends nothing from the member's account (P2-AC-12,
  `TestDecideState_Row4UsesOnlyTheOrganisation`, `backend/nlp/llm_factory_p2_test.go:116`).
- No key in error text, logs, reports or operator records (P2-AC-39,
  `TestPAC73P2_NoKeyLeavesThroughProviderErrorText`).
- PAC-84: the organisation's key and members' keys stay sealed; a kept key is
  written back byte-identical (`org_ai.go:434-444`), and members' still-stored
  keys in row 9 remain sealed and unread except for redaction.
- PAC-50 AC-27: an SSO grant is never spent on an organisation-AI change, and
  the reverse.

## 7. Rollback

Reverting restores "any provider, key or destination change re-asks" and
"forbidden own-provider members stay off". The data change at deploy is
additive (a new consent-period marker per organisation, confirmations
re-keyed on it). The down migration must not let a confirmation be read as
covering a configuration it did not confirm; the simplest compliant down
migration ends every stored confirmation, so every follower and moved member
is asked once more after a rollback and nothing is sent before they confirm
(PAC87-AC-19). It loses only confirmations (no user data, no keys, no stored
choices). Confirmations ended at deploy under P87-D5 are not restored by a
rollback; they had no effect under the shipped rules either.

## 8. Open questions

No product-level question blocks this spec: both owner answers are applied as
written and neither conflicts with the consent rule O-13 (OQ-P2-2 itself says
"after confirming once").

Factory-assumed decisions the owner may overrule (each has a falsifiable AC,
so overruling changes a test, not the design):

| ID | Decision | Why | AC |
|---|---|---|---|
| P87-D1 | Removing the configuration, and P → Q → P, count as provider changes and re-ask | literal reading of "the provider changes"; conservative for consent | AC-3, AC-4 |
| P87-D2 | A moved member's confirmation survives lifting and re-imposing the ban if the provider did not change | OQ-P2-1: only a provider change re-asks | AC-9 |
| P87-D3 | Only a settings write that *changes* the stored choice to off or another provider ends the confirmation | otherwise unrelated saves undo the move and break "ask once" | AC-13 |
| P87-D4 | Confirmations of older configurations are not revived at deploy | history not reliably stored; matches shipped behaviour | AC-18 |
| P87-D5 | Confirmations held by non-followers are ended at deploy | so every moved member sees row 10's text before being moved | AC-18 |
| P87-D6 | The admin form tells the admin before saving whether members will be asked again | makes OQ-P2-1's consequence visible to the person causing it | AC-17 |

For the contract stage (owner: contract-author), not the owner:

- How the read exposes the consent period and what the confirmation names
  (the issue proposes keeping the configuration version for administrators
  and adding a separate number for consent; a confirmation naming the version
  shown is also workable if the server compares providers). AC-5 is the
  behaviour either must meet.
- Whether row 10 keeps its machine-readable reason (with a now-confirmable
  meaning) or gets a new one; clients today read `personal_keys_forbidden`
  + `enablers: [user]` as "offer Use my organisation's AI"
  (`aiProviders.ts:120-125`), so a reused reason changes their meaning
  silently. The contract must say which and the web must follow.
- The AI status for a member in row 9 combines a stored own provider with
  "configured: the organisation's". The contract's `Settings.llmProvider` and
  `AIStatus` descriptions (rows 10–11 only, today) must cover it.
- The contract text that says "Writing `""` or a provider deletes the
  caller's organisation confirmation" and "re-entering the same key re-asks
  members" (`scheduling.yaml:1180-1195`) must be rewritten to §3.1 and §3.4.
