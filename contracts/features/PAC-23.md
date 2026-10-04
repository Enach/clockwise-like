# Contract — PAC-23: the activity log must belong to the person it is about

- **Linear issue**: [PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the)
- **Spec**: `docs/specs/PAC-23.md`
- **Manifest**: `contracts/features/PAC-23.yaml` (revision 2; bundle
  `23099fbcacc5efbdd18af03ca73ef98534f44e2464e384716eeb5219020a627c`, fragment
  `568941e4687ef1e9ebd38a21dc2a0037fa459665368bac9c8d0883be7b4c2bb5`). Revision 1's
  `2ae42114…` was stale — see Revision 2 §U2 below.
- **Stage**: contract-author → contract-challenger (revision 2, after a `reject`)
- **Source of truth repo**: `Enach/clockwise-like`
- **Resolves**: API-005 (critical), API-074 (low), `x-uncertain` U-02
- **Operations changed**: exactly one — `listAuditEntries` (`GET /api/audit`)

> Nothing in this document was verified by executing the backend; the module proxy is
> blocked in this sandbox. `python3 scripts/openapi_assemble.py` and
> `scripts/openapi_migration_report.py` were run and their output is in the Linear
> comment. Everything else is from reading source.

---

## 1. What changed, in one paragraph

`GET /api/audit` stops being a global read and becomes a read of the caller's own
entries. No request body, no path, no new parameter, no new response code and **no
change to the shape of `AuditEntry`**. The entire change at the HTTP boundary is: the
same request now returns a different, smaller set of rows, determined by who is
asking; the `limit` default moves from 100 to 50; and an over-large `limit` caps
instead of resetting. The `x-uncertain` marker U-02 is removed because the question it
asked has been answered, not because the marker was inconvenient.

## 2. Why the response shape does not change

The obvious contract for "scope this to the caller" adds a `user_id` to `AuditEntry`,
and this contract deliberately does not.

Every entry in every response has the same subject — the caller — because that is the
predicate that produced the response. A field whose value is identical on every row of
every response, and which the client already knows because it is the authenticated
user, carries no information. It costs a schema change, a regenerated type in both
repos, a normaliser branch in `src/api/client.ts:422-439`, and an `additionalProperties:
false` schema that now admits a field nobody reads.

It costs something else too, which matters more. A `user_id` on the wire invites a
`?user_id=` beside it, and a filter parameter on an endpoint whose whole defect was the
absence of a filter is a trapdoor: the first request to add "just for managers" lands
on a parameter that already exists. Spec §3.1 requires that *no request a user can
compose returns another person's row*. The cheapest way to honour that is for the
interface to have no vocabulary for addressing another person at all.

**Rejected alternative A — add `user_id` to `AuditEntry`.** Rejected for the above.
Note the consequence being accepted: the wire format cannot distinguish "the server
scoped this correctly" from "the server returned everything and the client happened to
be alone in the deployment". The scoping is therefore proven by AC-1/AC-3/AC-4 —
tests that use two users — and not by inspecting a single response. That is the right
place to prove it regardless; a `user_id` field would have made a single-response test
*look* sufficient while proving nothing about what was filtered out.

**Rejected alternative B — a `scope=me|org` parameter, defaulting to `me`.** It
anticipates OQ-1, which has not been decided, and it would ship a documented
`scope=org` the server must reject — the factory's own preference is to make the wrong
request impossible to express rather than to document its rejection. If OQ-1 later
says yes, an org view is a different operation with a different audience and probably a
redacted projection (spec §3.5), not a query parameter on this one.

## 3. Why pre-migration rows are described in the contract rather than quietly filtered

The description now states that rows with no subject are returned to nobody and are
retained. That is unusual for an OpenAPI description, and it is there because it is the
only place a consumer will look when their log goes empty on release day.

A contract that said only "returns the caller's entries" would be true and would leave
the single most surprising observable behaviour of this release undocumented. The
factory's rule is that the contract describes reality; on the day this ships, reality
includes several hundred rows that exist, are ordered, are within the limit, and are
returned to nobody.

**Rejected alternative C — delete the pre-migration rows in the migration**, as
`docs/factory/api-audit.md:509-510` suggests. Argued in spec §3.3. In contract terms
the objection is narrower: deleting them makes the *description* simpler and the
*rollback* lossy in a way the manifest would then have to admit. Retaining them costs
one sentence here and nothing anywhere else.

**Rejected alternative D — backfill by inference.** Spec §2.5 establishes it is
infeasible for five of seven action types and heuristic for the other two. A contract
that promised "your entries" over rows attributed by timestamp proximity would be a
lie in the specific way the factory §7 warns about.

## 4. The `limit` parameter (API-074)

Three numbers were in play: the contract said the default was 100, the server behaved
as 100, and the only shipped client defined 50 and always sent it. Nothing was broken,
because the client always sent a value — which is precisely why this survived.

The contract now declares **one** default, 50, and the manifest requires the client to
take it from the generated contract rather than declare its own. 50 rather than 100 is
chosen because it is the number users actually experience today
(`smart-calendar-flow/src/pages/Audit.tsx:39-40` says "the last 50 actions"), so
aligning downward changes nothing anyone sees, while aligning upward would change the
page for every user as a side effect of a security fix.

The second half is the clamp. `storage.ListAuditLog` replaces any value `> 500` with
**100** (`backend/storage/audit_log.go:16-18`) — a number below the declared maximum.
A caller asking for more than is allowed should receive the maximum, not an arbitrary
smaller number, so the contract now says it caps. The schema keeps `minimum: 1,
maximum: 500`, so a conforming client cannot express the out-of-range request in the
first place; the described behaviour covers the non-conforming one.

**Not done:** rejecting an out-of-range `limit` with a 400. It would add a response
code to an operation whose only consumer never sends one, for no benefit over capping.

## 5. What the contract does not say, on purpose

- **It does not describe the column, the migration, or `WriteAuditLog`'s signature.**
  Those are the plan's business. The HTTP boundary is unaffected by how the subject is
  stored.
- **It does not promise that `details` is parseable.** It now warns the opposite, with
  the three concatenating call sites cited, because that defect is live on the
  operation this feature touches and is not being fixed here (spec OQ-6). Per factory
  §7 the contract says so rather than describing the intended behaviour.
- **It does not describe an actor distinct from the subject.** Spec OQ-2 is open; today
  they are always the same value (spec §2.4) and a contract that distinguished them
  would be unfalsifiable.
- **It does not claim this is an audit trail.** The description says in as many words
  that it is a per-user activity feed and points at spec §3.5. This is a contract
  statement, not a comment, because the next agent to look for "where do we record who
  changed the SSO configuration" will read this operation first and must not build on
  it.

## 6. Generated-code status

`listAuditEntries` stays `handwritten` in `contracts/openapi/MIGRATION.md:75`.

Factory §4 says a modified endpoint moves to the generated server interface. Zero of
119 operations are `generated` today and `backend/api/gen/` does not exist, so
honouring that rule here means introducing `oapi-codegen`, the generated
`StrictServerInterface`, the mounting wrapper and the first migrated handler **inside a
critical security fix** — which is the kind of bundling that makes a revert dangerous
and a review impossible.

This contract therefore leaves the row as `handwritten` and escalates rather than
decides: spec OQ-5, owner = whoever owns `MIGRATION.md`. If the answer is "no
exception", stage 3's plan grows a whole workstream and the sequencing in
`docs/factory/PAC-23-plan.md` §5 has to be redone before implementation starts. The
`make openapi-check` gate compares the generated-row count against the committed file
and only fails when it *decreases*, so leaving it handwritten does not break the gate.

## 7. What a challenger should attack

Offered specifically, because "looks good" is not a review:

1. **A request this contract permits that the server should reject.** `?limit=0` is not
   expressible under `minimum: 1`, but nothing stops a client sending it; the
   description says it resolves to the default. Is silently accepting it right, or
   should it 400? I chose consistency with absent-and-unparseable.
2. **A response this contract permits that the client cannot render.** `details`
   containing malformed JSON (§5). `formatAuditDetails`
   (`smart-calendar-flow/src/api/client.ts:410-419`) stringifies rather than parses, so
   I believe it renders — AC-15 is the guard, and I could not execute it.
3. **The consumer I may have missed.** I searched both repos, `mcp/` and `e2e/`. Spec
   OQ-4 covers out-of-tree callers and I cannot close it from here.
4. **The strongest objection to the whole shape**, which I will state rather than wait
   for: after this change nobody but the subject can read a subject's log, so a
   compromised account's own log is the only record of what was done with it. That is
   an argument for an org view, i.e. OQ-1, and against nothing in this contract — but a
   challenger should press on whether shipping per-user scope now makes the org view
   harder to add later. I do not think it does: the org view needs an administrator
   concept that does not exist, and it would read a different, redacted projection.

---

## Challenge — 2026-09-17

*Reviewed by `contract-challenger`, separately from the spec review and without the
author's reasoning. The Go toolchain and the npm registry are blocked here, so no handler
was executed and no test was run. `python3 scripts/openapi_assemble.py` runs and I ran it;
its exact output is below. Every other claim is from reading source, with file and line.*

**Verdict**: reject

Three specific edits from accept, and I am rejecting rather than accept-with-changes for
one reason: the `limit` description contains two clauses that give **different answers to
the same request**, and `limit` is the one behaviour this feature deliberately changes.
Stage 4 would have to guess which clause is normative, and the plan has already guessed
(`docs/factory/PAC-23-plan.md:70` and `:77`). A contract that an implementer must
disambiguate is the failure mode this gate exists to catch.

The shape of the change is right, and I want to say so before the findings: declining to
add `user_id` to `AuditEntry`, and declining a `scope=me|org` parameter, are both correct
and the reasoning in §2 is the strongest part of this artifact. "The cheapest way to
honour [§3.1] is for the interface to have no vocabulary for addressing another person at
all" is the right instinct and I could not break it — see Verified clean.

### Requests wrongly permitted

1. **`GET /api/audit?limit=10000`** — the contract gives two answers.
   `contracts/openapi/paths/calendar.yaml:722-731` says, in one sentence: *"Absent,
   unparseable **or out of range** resolves to the default of 50"*, and in the next clause:
   *"a value above the maximum is capped at the maximum rather than being rejected or
   silently reset to a smaller number."* `10000` is out of range **and** above the maximum,
   so the description permits both `50` and `500`.
   The schema (`minimum: 1, maximum: 500`) means a conforming client cannot send it, but
   nothing rejects it: `handlers_audit.go:14` discards `strconv.Atoi`'s error and
   `audit_log.go:16-18` is `if limit <= 0 || limit > 500 { limit = 100 }` — today the answer
   is a third number, `100`, which is the API-074 defect this feature exists to close.
   Spec AC-11 and the plan's `TestListAuditEntries_OverMaximumLimitCaps` both assume `500`.
   *Fix (schema/description)*: delete "or out of range" from the first clause and state the
   three cases disjointly — absent or unparseable → `50`; a value below `minimum` → `50`; a
   value above `maximum` → `500`. As written the parameter cannot be implemented from the
   contract alone.

2. **`GET /api/audit?limit=-5`** and **`GET /api/audit?limit=0`** — neither is expressible
   under `minimum: 1`, and the description covers them only via the "out of range" clause
   that finding 1 removes. Today both reach `limit <= 0` and become `100`
   (`audit_log.go:16`). The author raises `limit=0` in their own §7 item 1 and argues for
   consistency with absent-and-unparseable; I agree with the position, but the contract
   does not currently state it in a way that survives fixing finding 1.
   *Fix*: covered by the disjoint restatement above.

3. **`GET /api/audit?limit=abc`** — permitted by the wire, `Atoi` errors, error discarded,
   value becomes `0`, resolves to the default. The description says exactly this and it is
   correct. Recording it because it is the one out-of-schema input the contract handles
   unambiguously.

4. **A request this contract does not cover but this feature makes newly relevant**:

   ```
   POST /api/schedule/compress/apply
   Content-Type: application/json

   {"proposals":[{"event_id":"x\",\"title\":\"injected\",\"pad\":\"",
                  "proposed_start":"2026-09-17T09:00:00Z",
                  "proposed_end":"2026-09-17T09:30:00Z"}]}
   ```

   `event_id` is copied verbatim from the body into `engine.MoveProposal`
   (`backend/api/handlers_schedule.go:80-93`, no validation) and concatenated unescaped
   into `details` at `backend/engine/compression.go:178`. The stored row becomes
   `{"event_id":"x","title":"injected","pad":"","new_start":"…"}` — the caller has written
   *structure*, not just a stray quote.
   This does **not** break scoping: the row is still the caller's own, so spec AC-3 holds
   and `/api/audit` is not the vector. I am recording it because the contract's `details`
   description (`calendar.yaml:1337-1348`) attributes the defect to "a meeting title
   containing `\"`" at all three cited sites, and at `compression.go:178` there is no title
   — the injectable value is a client-supplied `event_id`. If the contract is going to
   carry a permanent "do not parse this" warning, the warning should describe the mechanism
   it actually has.

### Responses the client cannot handle

1. **`listAuditEntries` may return `action` values that the contract says are impossible.**
   `calendar.yaml:1334-1336` describes `action` as a *"Dotted action key, e.g.
   `settings.update`, `event.delete`."* Neither example exists anywhere in `backend/`. The
   complete set of values the server can produce is seven, all underscore-separated, and I
   enumerated them from the writers:

   | value | written at |
   |---|---|
   | `focus_created` | `backend/engine/focus_time.go:106` |
   | `focus_cleared` | `backend/engine/focus_time_cleaner.go:39` |
   | `meeting_scheduled` | `backend/engine/smart_schedule.go:323` |
   | `meeting_moved` | `backend/engine/compression.go:178` |
   | `meeting_created` | `backend/api/handlers_schedule.go:188` |
   | `nlp_confirmed` | `backend/api/handlers_nlp.go:67` |
   | `nlp_parsed` | `backend/nlp/parser.go:221` |

   This is not a stale description someone else left behind that PAC-23 may ignore. The
   manifest claims this schema as changed — `contracts/features/PAC-23.yaml:58-62`,
   *"AuditEntry … Documentation only"* — so the author edited this schema's documentation
   and left a false sentence standing in it, on the one operation this feature touches.
   Factory §7 rule 1 puts it in scope; factory §7 rule 2 makes it worse than a gap, because
   the contract is currently asserting something about the server that is not true.
   The fiction has already propagated downstream: `smart-calendar-flow/src/api/audit.test.ts:50`
   asserts against `{ id: 3, action: "focus.run", … }`. That test passes — it mocks `fetch`
   — so nothing will ever catch it. A consumer writing a per-action icon map or filter from
   this description gets seven misses.
   *Fix (schema)*: replace the description with the seven actual values. Either
   `enum: [focus_created, focus_cleared, meeting_scheduled, meeting_moved, meeting_created,
   nlp_parsed, nlp_confirmed]`, or keep `type: string` and list them as the current set with
   an explicit statement that it is open — but not a dotted-key convention that no writer
   uses.

2. **`listAuditEntries` may return an `id` the client silently corrupts.**

   ```json
   [{"id": 9007199254740993, "action": "meeting_created",
     "details": "{\"event_id\":\"a\"}", "created_at": "2026-09-17T09:00:00Z"}]
   ```

   `format: int64` (`calendar.yaml:1331-1333`) permits it.
   `normalizeAuditEntries` does `Number(e.id)` (`smart-calendar-flow/src/api/client.ts:432`)
   and `Audit.tsx:95` uses the result as the React list `key`, so two ids above 2^53 collapse
   to one key and React drops a row. The server cannot actually produce this — `audit_log.id`
   is `SERIAL`, i.e. 32-bit (`backend/storage/migrations/001_initial.up.sql:45`) — so the
   contract **over-declares** what the table can hold.
   *Fix (schema)*: `format: int32`, matching `SERIAL`. Low severity, but this is a schema the
   feature already touches and the generated TypeScript type is produced from it.

3. **`details: ""`** — permitted (`type: string`, required, no `minLength`) and genuinely
   produced (`audit_log.details` is `TEXT NOT NULL DEFAULT ''`, `001_initial.up.sql:47`).
   `Audit.tsx:100` guards with `{entry.details && …}`, so it renders as an absent line rather
   than an empty paragraph. **Renders correctly** — recording it because "required but
   possibly empty" is the shape that usually bites here.

4. **`[]`** — the release-day state for every user, and AC-2. `Audit.tsx:82-89` renders "No
   activity yet" when `!isLoading && !error && entries.length === 0`. **Renders correctly.**
   One consequence the contract's release-day paragraph (`calendar.yaml:709-716`) should
   know about: `api.getAudit` wraps the call in `withFallback` and returns `mockState.audit`
   when the backend is unreachable (`client.ts:1060-1067`), and `mockState.audit` is `[]`
   (`client.ts:146`). So a correctly-empty scoped log and a dead backend produce a
   byte-identical response and an identical panel. Not a contract defect — the contract
   cannot describe a client fallback — but it means the empty state carries no information
   on exactly the day the contract predicts everyone will see it.

5. **Malformed-JSON `details`, e.g. `"{\"event_id\":\"a\",\"title\":\"1:1 re: \"PIP\"\"}"`**
   — the author's own §7 item 2. I checked the consumer: `formatAuditDetails`
   (`client.ts:410-419`) returns a `string` input unchanged and never calls `JSON.parse`;
   `Audit.tsx:100-102` renders it as text. **It renders.** The author's belief is correct
   and AC-15 is the right guard. Verified so the next reader need not.

### Consumers affected

- **`smart-calendar-flow/src/pages/Audit.tsx:22`** — `useAudit(DEFAULT_AUDIT_LIMIT)`. Sees
  only its own rows. No change needed for scope. Note `Audit.tsx:38` interpolates
  `DEFAULT_AUDIT_LIMIT` into the visible subtitle, so the contract's declared default is
  also user-facing copy — §4's argument for choosing 50 over 100 is right, and stronger
  than it states.
- **`smart-calendar-flow/src/components/QuickActions.tsx:20`** — `useAudit(10, showAudit)`.
  A hardcoded `10`, but an *explicit* request, not a second default, so AC-10 is unaffected.
  The manifest reaches it correctly via `src/hooks/useAudit.ts`.
- **`smart-calendar-flow/src/api/client.ts:403-404, 1060-1067`** — `DEFAULT_AUDIT_LIMIT` and
  `getAudit`. The API-074 change lands here.
- **`smart-calendar-flow/src/api/audit.test.ts:50`** — encodes `action: "focus.run"`,
  downstream of the false `action` description (finding 1). It mocks `fetch`, so it will
  keep passing while being wrong.
- **`mcp/`** — none. I re-ran the search rather than trusting the manifest: no match for
  `audit`, `WriteAuditLog`, `SmartScheduler`, `CompressionEngine`, `NLPService` or
  `FocusTimeEngine` anywhere under `mcp/`. The manifest's claim is correct.
- **`e2e/`** — none today. Correct.
- **Note on the brief I was given**: `src/api/audit.ts` does not exist. The manifest does
  not cite it; `contracts/features/PAC-23.yaml:37` lists `src/api/audit.test.ts` and
  `src/api/client.ts`, which is right.

### Contract/implementation divergences

1. **Contract**: `action` is a "Dotted action key, e.g. `settings.update`, `event.delete`"
   (`calendar.yaml:1334-1336`). **Handler**: produces seven underscore-separated values,
   none of them either example, at the seven `WriteAuditLog` call sites listed above.
   This is a live divergence on a schema the manifest claims to have edited. Blocking.

2. **Contract**: `id` is `format: int64` (`calendar.yaml:1331-1333`). **Table**:
   `id SERIAL PRIMARY KEY`, 32-bit (`001_initial.up.sql:45`). Over-declared.

3. **Contract**: describes the scoped read, the 50 default and the cap as present tense.
   **Handler**: `handlers_audit.go:12-23` and `audit_log.go:15-34` do none of it yet — no
   `WHERE`, default 100, `>500` resets to 100.
   I considered raising this under factory §7 rule 2 ("the contract describes what is") and
   decided it is **not** a finding: rule 2 governs defects the feature is *not* fixing, and
   API-005/API-074 are precisely what it is fixing. The contract is allowed to be the
   thing stage 4 implements against. I am recording the reasoning so it is not re-litigated.
   By the same logic the `details` warning is correctly in the present tense, because that
   defect is *not* being fixed — see the judgement below.

4. **Not a divergence, checked**: `security`. `/api/audit` carries no `security: []`
   override, so it inherits the document-level requirement at `openapi.yaml:24`, and the
   assembler classifies it among the authenticated operations. `requireAuth`
   (`middleware.go:36-53`) sets `Content-Type: application/json` and then calls
   `http.Error`, which overwrites it with `text/plain; charset=utf-8` while emitting the
   JSON literal body — which is exactly what the `MiddlewareUnauthorized` schema describes.
   AC-12 is honoured by the contract as written.

### Uncertainties not actually resolved

1. **U-02 is genuinely resolved, and I tried to argue otherwise.** The marker asked whether
   the global audit log was intentional. It is gone from `/api/audit` in `calendar.yaml`,
   and the evidence offered — the register rating it API-005 *critical*
   (`docs/factory/api-audit.md:239`) plus the only consumer advertising per-user scope at
   `Audit.tsx:38` — does settle the question the marker asked. This is a determination, not
   a deleted marker. No finding.
   The one honest caveat: nothing *pins* it yet. AC-1/AC-3/AC-4 are the pins and they do not
   exist. That is acceptable at stage 2 and would not be at stage 4.

2. **The manifest's recorded counts and hash are stale, and the hash is asserted as fact in
   three artifacts.** `contracts/features/PAC-23.yaml:6`, `PAC-23.md:5-6` and
   `docs/factory/PAC-23-plan.md:4-5` all pin
   `contractHash: 2ae421142e7d17eca73c625af3b8f1bb2c397e8bfd92f1fedde4d29377804965`.
   The bundle in this tree is `6e2bb0fcc5ed6a57b6bc75a3b89fd4246f0afb4c98051ff8141f1206a4d316cf`.
   The assembler now reports **120 operations** and **18 `x-uncertain`**, where the contract,
   the plan and the Linear comment all say 119 operations and a fall from 22 to 21.
   This is **not PAC-23's doing** — other agents have edited fragments in this tree
   concurrently, and `calendar.yaml` still carries exactly four `x-uncertain` markers. But
   three committed artifacts state a false hash and a false count as established fact, and
   `scripts/openapi_assemble.py` computes no hash, so nothing will ever catch it.
   *Fix*: re-record both against the bundle at merge, or — better, and the reason I am
   raising it rather than waving it through — stop pinning a whole-bundle hash from a
   feature manifest at all. A feature that owns one fragment cannot keep a hash of a
   document six other features are editing; the hash will be wrong at every merge and will
   train readers to ignore it.

3. **API-005 and API-074 are still open in the register.** `docs/factory/api-audit.md:239`
   and `:308` carry them as `Confirmed` and `Reported`, and `:446` still lists U-02. The
   plan schedules the bookkeeping for stage 4 (`PAC-23-plan.md:119`), which is a reasonable
   place for it, but the manifest's `resolvesFindings` currently claims a resolution the
   register does not yet reflect. Recording it so the stage-4 reviewer checks it.

### Verified clean

Attacked and could not break — the next reader need not repeat these:

- **The core design decision: no `user_id` on `AuditEntry`, and no `scope` parameter.** I
  tried to construct a request that addresses another user's rows under this contract and
  there is none: one operation, one optional integer parameter, no path template, no body,
  no filter. The author's argument — that a field on the wire invites a parameter beside it
  — is correct, and the accepted consequence (a single response cannot prove scoping, so
  AC-1/AC-3/AC-4 must use two users) is the right trade and is stated explicitly in §2.
  Rejected alternatives A and B are rejected for real reasons.
- **The response is a bare array, and the consumer tolerates more than the contract
  promises.** `normalizeAuditEntries` (`client.ts:421-441`) additionally unwraps `{entries:…}`
  and `{items:…}` envelopes and defaults every field. The contract declares only the bare
  array, which is what the handler emits (`handlers_audit.go:20-21`). Tolerance in the
  client, not looseness in the contract. Fine.
- **`additionalProperties: false` on `AuditEntry`** is safe: the handler encodes
  `storage.AuditEntry`, whose four fields are exactly the four declared
  (`backend/storage/audit_log.go:8-13`).
- **The `500` response.** `writeError(w, "audit: "+err.Error(), 500)` emits
  `{"error":…}` as JSON, which is `ErrorResponse`. Matches.
- **Retained NULL-subject rows are unreachable through any other query path.** I enumerated
  every SQL reference to the table in `backend/`: one `INSERT`
  (`storage/focus_blocks.go:62`), one `SELECT` (`storage/audit_log.go:19`), and the
  `CREATE`/`DROP` in `001_initial`. No view, no join, no second reader, and no `UPDATE`
  anywhere. Once the `WHERE` lands, the retained rows are genuinely returned to nobody —
  the contract's claim at `calendar.yaml:709-716` holds.
- **The manifest's `acceptanceTests` match the spec's acceptance criteria.** I compared all
  fifteen one by one. No silent narrowing, no dropped criterion, no criterion weakened
  between stages. This is the check that most often fails here and it passes.
- **`rollback` in the manifest states the data loss.** `contracts/features/PAC-23.yaml:112`
  says the down migration "irrecoverably destroys every attribution written since the up
  migration" and that a down-then-up cycle leaves the table permanently unattributed. Spec
  §7 and plan §6 say the same. The requirement that the plan must admit the loss is met,
  in all three artifacts, unambiguously.
- **The generated-code row.** `listAuditEntries` is `handwritten` at
  `contracts/openapi/MIGRATION.md:75`, and §6's claim that the gate only fails when the
  generated count *decreases* is consistent with leaving it there. The escalation is
  correctly an escalation and not a decision.

### On the `details` warning: is documenting an unparseable field acceptable?

Asked directly, so answered directly. **Documenting it is correct and required** — factory
§7 rule 2 is unambiguous, and a contract that typed `details` as parseable JSON would be a
lie that the three concatenating writers would immediately falsify. The contract does not
fail its purpose by saying so: its purpose is to let a correct consumer be written, and
"treat as opaque text, do not parse" is a usable instruction that the real consumer
(`formatAuditDetails`, `client.ts:410-419`) already follows.

**But the warning should not have to be permanent, and this contract cannot be the one to
retire it.** The fix is three string concatenations replaced by the marshalling the other
four writers already use, in three files `contracts/features/PAC-23.yaml:22-25` already
lists in `allowedPaths` because they need the `WriteAuditLog` signature change anyway. The
contract-author was right not to reach for it — factory §5 forbids `contract-author` from
changing behaviour not in the spec, and the spec put it in OQ-6. So the correction belongs
upstream: the **spec** brings the escaping defect into scope (my spec review raises this as
blocking finding 4, separately from the retention question OQ-6 bundles it with), AC-15
strengthens from "does not break" to "round-trips as valid JSON", and *then* this contract
drops the warning. Stage ordering intact, and the contract stops shipping a permanent
caveat that three lines would remove.

### Gate output

Run in this session, exactly as printed:

```
$ python3 scripts/openapi_assemble.py --check
OK: bundle is in sync (98 paths)
EXIT=0
```

```
$ python3 scripts/openapi_assemble.py
wrote contracts/openapi/openapi.yaml
  paths      : 98
  operations : 120  (14 public, 106 authenticated)
  schemas    : 146
  responses  : 11
  parameters : 8
  securitySchemes: 2
  x-uncertain: 18
EXIT=0
```

The gate **passes**. The three dangling `$ref`s the author reported in their Linear comment
(`LLMTestRequest`, and `SettingsValidationError` twice, all from `paths/scheduling.yaml`)
are gone — whoever owned that concurrent edit has since added the missing components.
PAC-23's `calendar.yaml` edits were never implicated and are not now. Note that the assembly
has moved since the contract was written: 120 operations and 18 markers, against the 119 and
21 recorded in the artifacts (see Uncertainties finding 2).

`make openapi-check`, `make contract`, `make verify`, `go build`, `go test` and anything npm
were **not run** and are not claimed — the Go and npm registries return 403 in this session.
Per factory §8 this is a stage 1–3 environment; those gates belong to a local session.

---

## Revision 2 — 2026-09-17

*By `contract-author`, answering the `contract-challenger` **reject** above. The Go
toolchain and the npm registry return 403 in this session, so nothing was compiled,
tested or executed. `python3 scripts/openapi_assemble.py` runs and I ran it; its exact
output is at the end. Every other claim is from reading source, cited to the line.*

Three blocking findings, all accepted. I did not accept any of them as stated without
re-deriving the answer myself, and in two places the answer is narrower or wider than
the challenge describes.

### R1 — `?limit=10000` had two answers. It now has one, and so do the other three cases

The challenger is right that the revision-1 description was self-contradicting, and
right that it is the worst possible place for that: `limit` is the one behaviour this
feature deliberately changes, so stage 4 would have had to pick a clause.

**What the server actually does.** I read both files rather than trusting either the
description or the plan.

- `backend/api/handlers_audit.go:14` — `limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))`.
  The error is discarded. An absent parameter and a non-numeric one are therefore the
  same input by the time anything looks at it: `Atoi` returns `0` with an error in both
  cases, and `0` is what reaches storage.
- `backend/storage/audit_log.go:16-18` — `if limit <= 0 || limit > 500 { limit = 100 }`.

So there is exactly **one** out-of-band outcome today, and it is `100`:

| request | value after `Atoi` | value after the clamp |
|---|---|---|
| `?limit` absent | `0` | **100** |
| `?limit=abc` | `0` (error discarded) | **100** |
| `?limit=0` | `0` | **100** |
| `?limit=-5` | `-5` | **100** |
| `?limit=10000` | `10000` | **100** |
| `?limit=50` | `50` | `50` |
| `?limit=500` | `500` | `500` |

Neither `50` nor `500` is reachable as a *resolution* today. The revision-1 description
offered both; the plan guessed `500`; the true answer was a third number that appeared
in neither. That is API-074 exactly, and it is why the finding was blocking rather than
cosmetic.

**What the contract now says.** Four disjoint cases, each stated once, with no overlap
and no input left uncovered: absent → 50; unparseable → 50; below `minimum` (which is
`0` and every negative) → 50; above `maximum` → 500. Nothing is rejected with a 4xx.
The phrase "or out of range" is gone, because it was the clause that made `10000` match
two rules at once.

**On factory §7 rule 2, which I had to think about rather than apply.** Rule 2 says the
contract describes what is, and a naive application of it here would have me document
`100` and stop. That cannot be right, or no contract could ever precede a behaviour
change and the factory's own ordering — contract, then plan, then code — would be
impossible. The challenger reached the same conclusion from the other direction and
recorded it (*"rule 2 governs defects the feature is not fixing, and API-005/API-074 are
precisely what it is fixing"*), and I am adopting that reading rather than re-litigating
it. But I am not relying on the reading alone: **the description now carries both**. The
contracted behaviour is stated as what stage 4 implements, and the current behaviour —
all four cases collapsing to `100`, with both file:line citations and the finding id — is
stated in the same parameter description, labelled as such. A reader who wants to know
what the deployed server does today gets a straight answer without leaving the contract,
and the contract asserts nothing false in the present tense. That is the only way I could
find to satisfy rule 2 and stage-4 implementability at once.

**Rejected alternative E — 400 on an out-of-range `limit`.** Still rejected, and the
challenger did not press it. It adds a response code to an operation whose only consumer
never sends one, and it would make `?limit=0` and `?limit=abc` behave differently from
each other for no reason a caller benefits from.

**Rejected alternative F — document `100` and leave the behaviour alone.** This is the
literal reading of rule 2 and it fails the spec: AC-10 requires the default to equal the
number the shipped client uses (50) and AC-11 requires an over-large request to return
the maximum. Documenting `100` would put the contract in direct conflict with two
accepted acceptance criteria, which is a worse lie than the one rule 2 guards against.

### P1 — the `action` enum is now the seven values, and it is a closed enum

The challenger's table is correct and I verified it independently rather than copying it.
`storage.WriteAuditLog` (`backend/storage/focus_blocks.go:61-63`) holds the only `INSERT`
against `audit_log`, and `action` is a **string literal** at every call site, so the set
is enumerable by grep and is complete:

| value | written at |
|---|---|
| `focus_created` | `backend/engine/focus_time.go:106` |
| `focus_cleared` | `backend/engine/focus_time_cleaner.go:39` |
| `meeting_scheduled` | `backend/engine/smart_schedule.go:323` |
| `meeting_moved` | `backend/engine/compression.go:178` |
| `meeting_created` | `backend/api/handlers_schedule.go:188` |
| `nlp_parsed` | `backend/nlp/parser.go:221` |
| `nlp_confirmed` | `backend/api/handlers_nlp.go:67` |

Two more call sites exist — `WriteAuditLog(db, "test_action", …)` and `"another_action"`
at `backend/storage/focus_blocks_test.go:105-106` — and they are excluded deliberately.
They are a test fixture writing into a testcontainers database; no deployed server can
emit them. If a contract test ever reads them back, the fixture is the thing to change.

**Why a closed `enum` and not `type: string` plus a list.** The challenger offered both.
I took the enum, and the argument is not "enums are cheaper than validation" — that rule
in `.claude/agents/contract-author.md` is about *requests*, and this is a response field,
where an enum constrains the server rather than the client. The real arguments:

1. It is what is. Seven literals, one insert path, no `CHECK` on the column but no way
   for another value to arrive either. An open set would be describing a looseness the
   code does not have.
2. It is the only version that is falsifiable. A prose list with "this set may grow" can
   never be wrong, which is precisely the property that let `settings.update` survive.
3. Where the fiction did damage is exactly where an enum helps: the challenger's example
   of "a consumer writing a per-action icon map or filter gets seven misses" is a
   consumer that wants a union type, and `openapi-typescript` produces one from an enum
   and `string` from prose.
4. The cost — an eighth action becoming a contract change — is the factory working. A
   new action is a new thing the product does; routing it through stage 2 is correct, and
   the alarm is a failing contract test rather than a silent divergence.

**The risk I checked before accepting that cost.** A closed enum on a response is
dangerous when a generated runtime validator rejects the whole response over one unknown
value. It does not here: the frontend's audit path is hand-written and does not validate.
`normalizeAuditEntries` (`smart-calendar-flow/src/api/client.ts:433`) does
`typeof e.action === "string" && e.action ? e.action : "unknown"` — it coerces with a
fallback and never compares `action` to a literal, so the generated union is assignable
everywhere it is used and an unexpected value degrades to `"unknown"` rather than
blanking the page. Recorded in the manifest's `schemasChanged.breaking` so stage 4 does
not have to rediscover it.

**The downstream copy: `smart-calendar-flow/src/api/audit.test.ts:50,54`.** It asserts
against `action: "focus.run"`. I read it: the literal is untyped input to a `fetch` mock,
so the enum does **not** turn it into a type error — it will keep compiling and keep
passing while documenting a value that cannot exist. It is recorded in the manifest as a
consumer that **must change**, and it belongs to **this feature's frontend stream, not a
separate issue**:

- the file is already in this manifest's frontend `allowedPaths` and the frontend stream
  must open it regardless for the `DEFAULT_AUDIT_LIMIT` change (AC-10);
- factory §7 rule 1 — touch it, fix it — applies, because the fiction sits on the one
  operation this feature modifies;
- and the decisive one: filing it separately means this PR deletes the *source* of the
  error while leaving a copy of it alive in a test that can never fail. A wrong statement
  whose origin has been removed is harder to find than one that still has a parent. The
  fix is one string literal (`focus.run` → `focus_created`).

Stated plainly because it is a gap: **no acceptance criterion covers this.** The
`contract-author` cannot add one — acceptance criteria are transcribed from the spec, not
invented here — so it is carried as a manifest-required consumer change and will be
visible to the stage-6 reviewer there.

### U2 — the hash, the counts, and why the bundle hash should not be in this manifest

**Algorithm.** `sha256` over the raw bytes of `contracts/openapi/openapi.yaml` as written
by `scripts/openapi_assemble.py`. This is not documented anywhere as a rule; I established
it by reproducing `PAC-24.yaml`'s recorded hash, which is the only manifest that states
its own method (`contractHashOf`, `PAC-24.yaml:7`): `sha256sum` of the pre-edit bundle
gave `6e2bb0fc…`, byte-identical to what PAC-24 recorded, which confirms both the
algorithm and the input. Reproduce with:

```
python3 scripts/openapi_assemble.py && sha256sum contracts/openapi/openapi.yaml
```

**Values at revision 2**, after this revision's `calendar.yaml` edits and a fresh assembly:

- bundle `contracts/openapi/openapi.yaml` — `23099fbcacc5efbdd18af03ca73ef98534f44e2464e384716eeb5219020a627c`
- fragment `contracts/openapi/paths/calendar.yaml` — `568941e4687ef1e9ebd38a21dc2a0037fa459665368bac9c8d0883be7b4c2bb5`

**The counts.** Revision 1 asserted 119 operations and a fall from 22 to 21 `x-uncertain`.
The assembler reports **119 operations** and **18 `x-uncertain`**. The operation count
matching revision 1's number is a coincidence and should not be read as vindication — see
the churn note below; when the challenger ran it, it was 120, and it was 120 for my first
two runs in this session. The marker count was never right. The 22 is worth explaining
rather than just correcting: `grep -c x-uncertain
contracts/openapi/paths/*.yaml` totals 22, but `count_uncertain`
(`scripts/openapi_assemble.py:228-242`) walks the **assembled** document, so fragment text
that does not survive assembly is not counted. Revision 1 appears to have counted the
fragments by hand and called it the bundle's number. The manifest now records the
assembler's figures and cites the function, and no longer predicts a post-merge count at
all.

**All three occurrences of the stale hash are fixed**, and I checked for a fourth:

| file | was | now |
|---|---|---|
| `contracts/features/PAC-23.yaml:6` | `2ae42114…`, revision 1 | revision 2 hashes, with `contractHashOf` stating the algorithm |
| `contracts/features/PAC-23.md:5-6` | `2ae42114…` | revision 2 hashes, pointing here |
| `docs/factory/PAC-23-plan.md:4-5` | `2ae42114…` | revision 2 hashes, with an inline note that the edit is factual only |
| `contracts/features/PAC-23.md:385` | `2ae42114…` | **left exactly as it is** — it is inside the appended Challenge section, where it is the challenger's quoted evidence. Correcting a quotation would destroy the record of the finding. |

`docs/factory/PAC-23-plan.md` is `plan-author`'s artifact and I edited one line of it,
which I would normally not do. The justification is narrow: the line asserts a hash that
never matched any assembled bundle, the challenger named it as one of the three false
assertions, and leaving it would mean the fix is incomplete by exactly the artifact the
implementer reads first. I changed nothing else — §2 and §5 remain `plan-author`'s, and I
have noted in the plan that the disjoint `limit` rules now match what §5's
`TestListAuditLog_DefaultAndClamping` and `TestListAuditEntries_OverMaximumLimitCaps`
already assert, so the plan's guess turned out to be right and needs no rewrite.

**The challenger's better suggestion: stop pinning a whole-bundle hash here.** I agree and
have half-implemented it. A feature that owns one fragment cannot keep a hash of a document
six other features edit, and I did not have to argue this hypothetically — **it happened
twice during this revision**:

1. `6e2bb0fc…` was already stale when the challenger read it, for edits PAC-23 never made.
2. I assembled after my `calendar.yaml` edits and recorded `532111ee…`, with 120
   operations. Minutes later, running the same command again for the gate output, the
   assembler reported **119 operations (14 public, 105 authenticated)** and the bundle
   hashed to `23099fbc…`. I changed nothing in between. PAC-24 removed an operation from
   `paths/scheduling.yaml` in this tree while I was writing this section.
   `contracts/openapi/paths/calendar.yaml` hashed to `568941e4…` before and after — **the
   fragment hash did not move, because PAC-23's surface did not move.**

So the recorded `contractHash` was falsified within one session by a change with no
relationship to this feature, while the fragment hash stayed a true statement about what
PAC-23 did. That is the whole argument, and it is now evidence rather than a prediction. A
hash that is wrong at every merge trains readers to ignore it, which is worse than not
having one. I could not simply drop the field:
`.claude/agents/contract-author.md:53` lists `contractHash` as required, and a
`contract-author` deleting a required manifest field to avoid a finding is the wrong
precedent. So the manifest now carries **both**, with `contractFragmentHashOf` saying in
as many words which one falsifies a change to this feature's surface. Making
`contractHash` fragment-scoped for every feature is a change to the manifest schema and to
the other four manifests; it is not PAC-23's to make, and it should be a factory issue.

### The three "also consider" items

**Public booking, and what `user_id` means when actor ≠ subject.** The spec challenger is
right that `POST /api/book/{slug}` is the genuine case: it is registered outside the
`requireAuth` group (`backend/api/routes.go:26`, under "Public booking routes — no JWT
required") and reaches `calClient.CreateEvent` at `backend/engine/booking.go:267`. So an
unauthenticated stranger creates a real event on someone's calendar and nothing is
recorded.

The contract's `user_id` semantics do **not** assume actor == subject, and the operation
description now says so rather than leaving it to be inferred. The answer to "how would a
public booking ever be recorded" is concrete: **the subject is the host**, and the host id
is already in scope at the call site — `booking.go:250-269` iterates `hosts` and already
persists `h.UserID` via `storage.SaveBookingEvent` two lines after the event is created.
No new plumbing, no nullable subject, no placeholder identity for the booker. The booker
is not a user of this system and must not be given a fabricated one (spec §3.2).

What the feed would then be unable to say is *who put the event there*, and that is the
sentence the description now carries: the absence of an actor field must not be read as a
claim that the subject acted. This is the concrete argument OQ-2 should be decided
against, and it is stronger than "a manager might one day act on a report's calendar" —
the divergent case is in production today and is invisible only because it writes nothing.
PAC-23 does not add the booking writer; that is out of scope and would need its own spec.

**`details`: I changed my position on the warning, but not on the type.** The challenger's
objection is that a contract declaring a field unparseable is close to useless to a
generator. I accept half of it. `type: string` is not the problem and does not change: it
is the honest wire type — `handlers_audit.go:21` JSON-encodes a Go `string`
(`storage/audit_log.go:11`) — and a generator emits `details: string`, which is correct,
complete, and exactly what the real consumer uses. A generator loses nothing. What was
useless was the *prose*: "treat this field as opaque text; do not parse it" is a blanket
prohibition that was wrong for four of the seven actions.

So the warning is now **per-writer**, which turns an unusable instruction into a usable
one:

- **Always valid JSON** — `focus_created` and `focus_cleared` (`encoding/json.Marshal`,
  `focus_time.go:101-106`, `focus_time_cleaner.go:33-39`); `nlp_parsed` (`fmt.Sprintf`
  with `%q`, which escapes, `nlp/parser.go:221`).
- **Not guaranteed** — `meeting_scheduled` (`smart_schedule.go:323`), `meeting_created`
  (`handlers_schedule.go:188`), `meeting_moved` (`compression.go:178`), `nlp_confirmed`
  (`handlers_nlp.go:67`).

**A correction to the spec, the register and my own revision 1: there are four
concatenating writers, not three.** `handlers_nlp.go:67` is
``` `{"event_id":"`+created.Id+`"}` ``` — the same unescaped construction. Spec §2.6 names
three and calls `nlp/parser.go` "the exception"; that is true of `parser.go` but skips
`handlers_nlp.go`. The input there is a Google-issued event id, which in practice contains
no JSON metacharacter, so the severity is genuinely lower — but the mechanism is identical
and a reader auditing the field should not be told there are three sites when there are
four. I cannot edit the spec; this is recorded here and in the schema description, and it
should be picked up when OQ-6 is split.

I also took the challenger's correction about **which** value is injectable where.
`compression.go:178` has no title: the interpolated values are `p.EventID` — copied
verbatim from the request body with no validation (`handlers_schedule.go:80-93`) — and an
RFC3339 timestamp. That site lets a caller write JSON *structure* into its own row, not
merely a stray quote, and the description now says that instead of repeating "a meeting
title containing a double quote" three times.

**Rejected alternative G — type `details` as a JSON-encoded object, or attach a schema for
its contents.** This is what would actually help a generator, and it is a lie for four of
the seven actions. Rule 2 applies with full force here, because PAC-23 is *not* fixing the
concatenation (spec OQ-6).

**Rejected alternative H — drop the warning entirely and fix the four writers in this
feature.** Tempting: the three files are already in `allowedPaths` for the
`WriteAuditLog` signature change, and it is four `json.Marshal` calls. Refused on the
role's own rule — `contract-author` may not change behaviour the spec does not ask for,
and the spec put this in OQ-6. The challenger reached the same answer and routed the fix
correctly: the **spec** brings the escaping defect into scope, AC-15 strengthens from
"does not break" to "round-trips as valid JSON", and *then* this description loses two
paragraphs. Stage ordering intact.

**`id` is now `format: int32`.** `audit_log.id` is `SERIAL`
(`001_initial.up.sql:45`) — a 32-bit signed sequence. `int64` over-declared what the table
can hold, and the consumer is the reason it matters rather than a pedantry: `client.ts:432`
does `Number(e.id)` and `Audit.tsx:95` uses the result as a React list key, so any id above
2^53 would collapse two rows onto one key and React would drop one. Every `int32` is
exactly representable as a JS number, so under the corrected declaration the coercion is
lossless by construction. The Go struct's `int64` (`storage/audit_log.go:9`) is a widening
in Go and not a range the database can reach; the schema describes the database. The
generated TypeScript type is `number` either way, so nothing downstream changes shape.

### Not changed, and why

- **No `user_id` on `AuditEntry`, no `scope` parameter.** The challenger attacked both and
  could not break either. Unchanged, and the reasoning in §2 above stands.
- **`listAuditEntries` stays `handwritten`** (`contracts/openapi/MIGRATION.md:75`). Still
  an escalation, spec OQ-5, now with the challenger's argument added to it: factory §4's
  rationale for incremental migration is that a big-bang move is a rewrite with no test
  coverage to catch what it broke, and §2.9 establishes this operation has **no** test at
  the HTTP boundary — which makes it the worst available pilot, not a neutral one.
- **API-005 and API-074 remain open in `docs/factory/api-audit.md`.** The challenger is
  right that `resolvesFindings` claims a resolution the register does not yet reflect. The
  plan schedules the register bookkeeping for stage 4 (`PAC-23-plan.md:119`), which is the
  correct place — the finding is resolved when the code lands, not when the contract
  describes it. Left as-is, flagged for the stage-6 reviewer.
- **The response is still a bare array**, `additionalProperties: false` is still on
  `AuditEntry`, and the `401`/`500` responses are unchanged. All verified clean by the
  challenger.

### Gate output

Run in this session, exactly as printed. `make openapi-check`, `make contract`,
`make verify`, `go build`, `go test` and anything npm were **not** run and are not
claimed — the Go and npm registries return 403 here. Per factory §8 this is a stage 1–3
environment.

```
$ python3 scripts/openapi_assemble.py
wrote contracts/openapi/openapi.yaml
  paths      : 98
  operations : 119  (14 public, 105 authenticated)
  schemas    : 146
  responses  : 11
  parameters : 8
  securitySchemes: 2
  x-uncertain: 18
EXIT=0
```

```
$ python3 scripts/openapi_assemble.py --check
OK: bundle is in sync (98 paths)
EXIT=0
```

No dangling `$ref` and no name collision. The three dangling `$ref`s from
`paths/scheduling.yaml` that revision 1's Linear comment reported are still gone.

`contracts/openapi/paths/scheduling.yaml` is owned by PAC-24 in this tree and I did not
touch it. The assembler reports no problem in it — and to be explicit, since the operation
count moved from 120 to 119 mid-session: that is PAC-24 removing an operation from its own
fragment, it assembles cleanly, and it is not a problem for me to fix or to report as one.

---

## Challenge, revision 2 — 2026-10-04

*By `contract-challenger`, against revision 2 of `contracts/features/PAC-23.yaml` and
the `/api/audit` surface in `contracts/openapi/paths/calendar.yaml`, without the
author's reasoning beyond what these artifacts say. Docker is not running here, so no
stack was started, no test was run and no database was queried.
`python3 scripts/openapi_assemble.py --check` and
`python3 scripts/openapi_migration_report.py --check` were run and their exact output
is at the end. I did **not** run the assembler in write mode (it would rewrite a
generated file), `make openapi-check`, `scripts/openapi_gen_go.sh --check`, `go build`,
`go test` or anything npm, and I claim nothing from them. Every other statement below is
from reading source, cited to the line.*

**Verdict**: reject

Four blocking findings. The reason this is a reject rather than an
accept-with-changes is **F3**: §6 and the manifest's OQ-5 both assert that
`backend/api/gen/` does not exist, and it does — `backend/api/gen/paceday.gen.go` has
been committed since `e9d01cc` and already contains this revision's `action` enum,
this revision's `int32` id, `ListAuditEntriesParams`, the request wrapper, the chi
mount for `/api/audit` and the strict response types, all regenerated from the bundle
by a script `make openapi-check` runs. So `listAuditEntries` has a **second server
artifact derived from this contract**, and that artifact answers
`GET /api/audit?limit=abc` with a `400` the operation does not declare (F1) and
`GET /api/audit?limit=10&limit=500` with a `400` the four-case list does not cover (F2).
Revision 1 was rejected because one request had two answers in one paragraph. That
paragraph is now correct and disjoint, and I want to say so plainly — but the same
request has two answers again, 50 in the prose and 400 in the code this contract
generates, and this time the contract does not contain both answers, it contains one
and generates the other.

Everything else holds up. The `limit` restatement is a real fix, not a paper one; the
seven-value enum is correct and I re-derived it; U-02 is determined rather than
deleted; and the refusal to put `user_id` or a `scope` parameter on the wire survived a
second attempt to break it. The fragment-hash argument is right about the diagnosis and
overclaims in the remedy — ruling in its own section below.

### Requests wrongly permitted

**F1 — blocking. `GET /api/audit?limit=abc` (also `?limit=`, `?limit=50.5`,
`?limit=1e2`) is answered `400` by the server this contract generates, and the
operation declares no `400`.**

The contract, `calendar.yaml:744-746`: *"Every input resolves; nothing here is ever
rejected with a 4xx. Each of the four cases below has exactly one outcome, stated
once."* `:749` then says *"Present but not a base-10 integer (`?limit=abc`) — 50"*, and
`:779-801` declares exactly three responses: `200`, `401`, `500`.

What the generated server does with that request:

- `backend/api/gen/paceday.gen.go:6287-6292` —
  `err = runtime.BindQueryParameter("form", true, false, "limit", r.URL.Query(), &params.Limit)`
  and, on error, `siw.ErrorHandlerFunc(w, r, &InvalidParamFormatError{ParamName: "limit", Err: err})`.
- The default `ErrorHandlerFunc`, `paceday.gen.go:9792-9794` —
  `http.Error(w, err.Error(), http.StatusBadRequest)`.
- The bind fails because `Limit` is `*int` (`paceday.gen.go:2579`), so the primitive
  branch of `BindQueryParameter` calls `BindStringToObject(values[0], output)`
  (oapi-codegen runtime v1.1.1, `bindparam.go:411`), which is `strconv` on `"abc"`.

So the response is:

```
HTTP/1.1 400 Bad Request
Content-Type: text/plain; charset=utf-8
X-Content-Type-Options: nosniff

error binding string parameter: strconv.ParseInt: parsing "abc": invalid syntax
```

This is not an incidental file in the tree. `make openapi-check` runs
`scripts/openapi_gen_go.sh --check` (`Makefile:96`), which regenerates
`backend/api/gen/paceday.gen.go` from the bundle into a temp dir and diffs it, so the
file is a gated artifact of this contract; and factory §3 rule 3 says that if a
generated file is wrong, the contract is wrong. Here the generated file is not wrong —
it is a faithful rendering of the schema. It is the prose that the schema cannot
express.

*Fix (schema)*: declare the status, using the convention this fragment already has for
exactly this class of input — `listCalendarEvents` at `calendar.yaml:63-70` declares
`"400"` with `text/plain` and `PlainTextError` for `invalid start param`:

```yaml
        "400":
          description: >-
            `limit` was present but not a base-10 integer, or was supplied more than
            once. Emitted by the generated request binder
            (backend/api/gen/paceday.gen.go:6287-6292 via the default
            ErrorHandlerFunc at :9792-9794), so the body is plain text, not JSON. The
            hand-written handler in place today does not produce this: it discards
            `strconv.Atoi`'s error (backend/api/handlers_audit.go:14).
          content:
            text/plain:
              schema:
                $ref: "#/components/schemas/PlainTextError"
```

and delete *"nothing here is ever rejected with a 4xx"*, or narrow it to the two cases
it is true of (below `minimum`, above `maximum`). What must not stay is a universal
claim about 4xx on an operation whose own generated binder 4xxes, with no `400`
declared. Note this does **not** reopen rejected alternative E: `?limit=10000` binds
fine as an integer and still resolves to 500 under the contracted behaviour. The 400 is
a *parse* failure, a case E never considered.

**F2 — blocking. `GET /api/audit?limit=10&limit=500` is not one of the four cases, and
the contract claims the four are exhaustive.**

Each value conforms to `minimum: 1, maximum: 500`, so the request is neither absent,
nor unparseable, nor below the minimum, nor above the maximum. The contract therefore
gives no answer to it while asserting at `:745` that each of four cases has *"exactly
one outcome, stated once"*. The two servers disagree:

- hand-written: `r.URL.Query().Get("limit")` (`backend/api/handlers_audit.go:14`)
  returns `values[0]`, so the answer is `10` and the second value is silently ignored.
- generated: the primitive branch returns
  `fmt.Errorf("multiple values for single value parameter '%s'", paramName)`
  (oapi-codegen runtime v1.1.1, `bindparam.go:398-400`) → `400`, as F1.

*Fix*: either add the missing sentence — *"Supplied more than once, the first value is
used and the rest are ignored (`r.URL.Query().Get`, backend/api/handlers_audit.go:14)"*
— or fold the case into F1's `400`. Pick one; the exhaustiveness claim at `:745` is
load-bearing and is currently false.

**F3 — blocking. §6 and the manifest's OQ-5 rest on a citation that is wrong:
`backend/api/gen/` exists.**

- §6: *"Zero of 119 operations are `generated` today and `backend/api/gen/` does not
  exist, so honouring that rule here means introducing `oapi-codegen`, the generated
  `StrictServerInterface`, the mounting wrapper and the first migrated handler inside a
  critical security fix."*
- manifest `openDecisions`, OQ-5: *"…when zero operations are generated and
  `backend/api/gen/` does not exist."*

The first half is true — `python3 scripts/openapi_migration_report.py --check` prints
`OK: MIGRATION.md covers all 119 operations (0 generated)`, and
`listAuditEntries` is `handwritten` at `contracts/openapi/MIGRATION.md:75`. The second
half is false. `backend/api/gen/paceday.gen.go` is 697 KB of committed generated code,
added by `e9d01cc` ("make the Go codegen, its drift check, lint and the test script
actually run"), and `backend/api/gen/README.md` states the policy in as many words:
*"The package is generated in full from day one; handlers adopt `StrictServerInterface`
one endpoint at a time."* For this operation specifically it already contains:

| artifact | line |
|---|---|
| `AuditEntryAction` constants, all seven | `paceday.gen.go:46-54` |
| `AuditEntry` with `Id int32` and `Action AuditEntryAction` | `:500-523` |
| `ListAuditEntriesParams` with `Limit *int` | `:2570-2580` |
| `ServerInterface.ListAuditEntries` | `:4971` |
| the request wrapper that binds `limit` | `:6270-6296` |
| the chi mount `r.Get(options.BaseURL+"/api/audit", wrapper.ListAuditEntries)` | `:9828` |
| `ListAuditEntries200JSONResponse` / `401TextResponse` / `500JSONResponse` | `:10510-10535` |

So the cost §6 prices — introducing the generator, the strict interface and the
mounting wrapper — is already paid, and paid in a way that regenerates on every
`make openapi`. What remains for this operation is a handler adoption. That is a
materially smaller decision than the one §6 escalates, and OQ-5 should be re-posed
against the tree as it is.

§6's description of the gate is also incomplete: *"The `make openapi-check` gate
compares the generated-row count against the committed file and only fails when it
decreases"* is true of `scripts/openapi_migration_report.py` only
(`openapi_migration_report.py:55-57`). The same target also runs the Go codegen drift
check (`Makefile:96`) and, when `WEB` is set, the frontend repo's own `openapi-check`
(`Makefile:98-99`).

I am marking this blocking rather than filing it as a correction because it is not
merely a stale fact: it is the premise that hid F1 and F2. An author who knew a
generated binder existed for this operation would have asked what that binder does with
`?limit=abc` before writing *"nothing here is ever rejected with a 4xx"*.

*Fix (document and manifest text)*: correct both sentences, and restate OQ-5 as what it
now is — whether the handler adopts the already-generated `StrictServerInterface` for
`listAuditEntries` — noting, as revision 2 already argues well, that §2.9 establishes
this operation has no test at the HTTP boundary and is therefore the worst available
pilot.

**F4 — blocking. The manifest's one concrete API-074 consumer change names an artifact
that does not exist in the frontend repo.**

Manifest `consumersAffected[0]`: *"One change required for API-074:
`DEFAULT_AUDIT_LIMIT` must come from the generated contract default rather than being a
hand-written 50 in `src/api/client.ts:404`."* AC-10 `[contract]` then requires that the
contract default and the number the shipped client sends are the same number.

`smart-calendar-flow/src/api/generated/` contains **only** `README.md`. There is no
`types.ts` and no `schemas.ts`; `grep -rn focus_created src/api/generated/` returns
nothing, and that repo's HEAD is `b6fee29`. The README documents both files as
generated and committed, and factory §4 lists them, but they have never been produced
in that tree. So the single consumer-side action the manifest offers for the finding it
claims to resolve cannot be performed as written, and the second "default" survives as
a hand-written literal — which is API-074 itself, with the two numbers now
coincidentally equal.

There is a second layer I could not settle here (see "What I could not settle"):
`openapi-typescript` emits types, not runtime values, so even once `types.ts` lands a
parameter `default` arrives as documentation rather than as a number a call site can
import. Only the zod `schemas.ts` could plausibly carry `50` as a value.

*Fix (manifest text, not schema)*: name the generated file and the exported symbol the
client is to read the default from, or drop the claim and state how AC-10's second half
is pinned instead — the cheapest honest version is a frontend test asserting
`DEFAULT_AUDIT_LIMIT` equals `paths["/api/audit"].get.parameters[limit].schema.default`
read from the committed bundle. As it stands AC-10 is a `[contract]` criterion whose
second clause nothing can satisfy.

**F5 — non-blocking. The contract asserts a storage fact that an open plan question can
falsify, in the same document that says it describes no storage.**

`calendar.yaml:709-716`: *"Rows written before the PAC-23 migration have no subject
(`user_id IS NULL`) and are therefore returned to **nobody**."* §5: *"It does not
describe the column, the migration, or `WriteAuditLog`'s signature. Those are the
plan's business."* It does describe the column, and the sentence is an identification —
`user_id IS NULL` ⟺ pre-migration — that `docs/factory/PAC-23-plan.md:320-324` leaves
open: the plan's own open question 1 asks whether the FK stays `ON DELETE CASCADE` (as
drafted at `:140`) or becomes `ON DELETE SET NULL`, and says SET NULL *"would keep the
rows but overload `NULL`"*. Under SET NULL the contract's sentence becomes false — a
deleted user's rows would also carry `user_id IS NULL`, would also be returned to
nobody, and would not be pre-migration. The contract is merged before that question is
answered.

Non-blocking because the plan as drafted chose CASCADE, under which the sentence is
true today, so nobody has to guess. But the contract should not be the artifact that
goes stale when a plan question resolves.

*Missing sentence* (replacing the parenthetical, not the paragraph): *"Entries recorded
before the PAC-23 migration have no subject and are returned to nobody; they are
retained rather than deleted. How 'no subject' is represented in storage is the
migration's business — a conforming server returns an entry only to its subject, and an
entry with no subject has none."*

### Responses the client cannot handle

**F6 — non-blocking. The contract permits a response that puts a third party's free
text in a subject's feed, and says nothing against it.**

The operation description now establishes, deliberately and correctly, that *"the
subject of an entry is whose calendar it happened to, which is not necessarily who
caused it"* (`calendar.yaml:721-737`), and names `POST /api/book/{slug}` as the
divergent path whose subject would be the host. The `details` description
(`calendar.yaml:1426-1465`) establishes that `details` carries free text supplied by
whoever made the request — a meeting title from the request body at
`smart_schedule.go:323` and `handlers_schedule.go:188`, the typed prompt at
`parser.go:221`. Put together, the contract permits:

```
GET /api/audit
Cookie: auth_token=<the host's token>

200 OK
[{"id": 214,
  "action": "meeting_created",
  "details": "{\"event_id\":\"abc\",\"title\":\"Dana — PIP discussion\"}",
  "created_at": "2026-10-04T08:00:00Z"}]
```

where the title was typed by somebody who is not the subject. No writer produces this
today — all seven have actor = subject, which I re-verified from the seven call sites —
so this is non-blocking. But the contract is the artifact that establishes
subject ≠ actor as a legal shape, and spec AC-3 only forbids the mirror image (another
user's text reaching a *non*-subject). The direction the contract has just opened is
unguarded.

*Missing sentence* (in the operation description, beside the actor paragraph): *"The
scoping guarantees who may read a row, not that the row's contents originated with the
reader. A writer whose actor is not the subject must not place text supplied by the
actor in `details`; where that is unavoidable the entry needs the redacted projection
spec §3.5 describes, not this operation."*

**F7 — non-blocking. The closed `action` enum has no stated degradation, and one bad
row invalidates the whole array.**

The enum (`calendar.yaml:1418-1425`) is right and I am not asking for it to be opened —
revision 2's four arguments for closing it are sound, and argument 3 in particular is
the one that matters. But the contract itself says the set is *"held by this contract
rather than by the database"* because `audit_log.action` is unconstrained `TEXT`. The
consequence it does not state: the 200 body is `type: array` of `AuditEntry`, so under
a validating consumer — and factory §4 mandates a generated zod `schemas.ts` in the
frontend — a single out-of-enum row makes the **entire response** non-conforming, not
that one row. One operator `INSERT`, one writer added without a contract change, or one
restored backup and the panel blanks rather than showing six good rows and one odd one.
The shipped client does the right thing (`client.ts:435` coerces to `"unknown"`), and
the contract should say that is the expected handling.

*Missing sentence*: *"A value outside this enum makes the response non-conforming as a
whole, because the body is an array of this schema. The shipped client coerces an
unrecognised value to `unknown` (smart-calendar-flow/src/api/client.ts:435) and renders
the row; a validating consumer should do the same rather than discard the response."*

Citation correction while here: the action coercion is at `client.ts:435`, not `:433`.
Revision 2's §P1 cites `:433` twice and the manifest's `schemasChanged.breaking` cites
it once. `:432` (`const idNum = Number(e.id)`) is correct, as cited in the `id`
description.

**F8 — non-blocking. `200 []` is pinned by the schema, but by a line the plan is about
to rewrite, and the contract does not say "never `null`".**

The bundle is `openapi: 3.1.0` (`openapi.yaml:4`), where `type: array` excludes `null`,
so a `null` body is already non-conforming and AC-2 is expressible. The reason it holds
in practice is `entries := make([]AuditEntry, 0)` at `backend/storage/audit_log.go:25`.
`docs/factory/PAC-23-plan.md:124` rewrites exactly that function
(*"`ListAuditLog` gains `userID uuid.UUID`, the query gains `WHERE user_id = $1`, the
clamp becomes default 50 / cap 500"*), and `var entries []AuditEntry` in place of
`make` emits `null`. The empty case is the release-day case for every user and the
contract already devotes a paragraph to it.

*Missing clause* in the `200` description (`calendar.yaml:780-784`): *"always a JSON
array — `[]`, never `null`, guaranteed today by `make([]AuditEntry, 0)` at
backend/storage/audit_log.go:25."*

Attacked and not broken, so the next reader need not: `[]` renders as "No activity yet"
(`smart-calendar-flow/src/pages/Audit.tsx:82-89`); `details: ""` is permitted,
genuinely produced (`TEXT NOT NULL DEFAULT ''`) and guarded at `Audit.tsx:100`;
malformed-JSON `details` renders, because `formatAuditDetails` (`client.ts:410-419`)
returns a string input unchanged and never calls `JSON.parse`. The previous challenge's
findings 3, 4 and 5 all re-verify.

**F9 — non-blocking. No pagination, and the contract does not say older activity is
unreachable.**

`limit` is capped at 500 and there is no `offset`, cursor or `before` parameter. This
operation is now, by the contract's own statement, the only way anyone can read what
happened to a user's calendar — *"it is readable only by its subject"* — and a user with
more than 500 recorded actions can never retrieve the 501st. That may well be the right
product answer; it is not stated anywhere, and the one audience that will hit it is the
one the contract's release-day paragraph is addressed to.

*Missing sentence*: *"There is no pagination. Entries older than the most recent `limit`
(at most 500) cannot be retrieved through this API at all."*

**F10 — non-blocking. The 401 matches byte for byte. Two clauses are missing, and the
generated 401 does not.**

Verified rather than assumed. `requireAuth` sets `Content-Type: application/json` and
then calls `http.Error` at `backend/api/middleware.go:36-38`, `:41-43` and `:46-48`;
Go's `http.Error` overwrites Content-Type with `text/plain; charset=utf-8`, sets
`X-Content-Type-Options: nosniff` and `Fprintln`s the message, so the body is exactly
`{"error":"unauthorized"}\n` — byte-identical to `MiddlewareUnauthorized`'s `example`
(`calendar.yaml:827-833`). AC-12 ("refused exactly as it is today") is honoured by the
contract as written. Two gaps:

1. `MiddlewareUnauthorized`'s description names the Content-Type but omits the
   `charset=utf-8` and the `nosniff` header that its sibling `PlainTextError` states
   (`calendar.yaml:819-826`). AC-12 says *exactly* as today, and those two are part of
   today.
2. `ListAuditEntries401TextResponse` (`paceday.gen.go:10519-10527`) sets
   `Content-Type: text/plain` with no charset, no `nosniff`, and writes the value
   verbatim without appending a newline. So the trailing `\n` is part of the *value* a
   generated handler must supply, and the generated 401 is not byte-identical to the
   middleware's unless it does. Worth one clause in the schema description, since the
   newline is load-bearing for AC-12.

### Consumers affected

Re-checked against the frontend tree at `b6fee29` rather than trusting the manifest.

- `smart-calendar-flow/src/pages/Audit.tsx:22, 38` — `useAudit(DEFAULT_AUDIT_LIMIT)`,
  and the number is interpolated into the visible subtitle. Sees only its own rows; no
  change for scope. The React list key is at **`Audit.tsx:94`**, not `:95` as the `id`
  description cites (`calendar.yaml:1371-1386`).
- `smart-calendar-flow/src/api/client.ts:404, 410-419, 422-441, 1060-1067` —
  `DEFAULT_AUDIT_LIMIT` is at `:404`; `Number(e.id)` at `:432`; the action coercion at
  `:435` (see F7); `getAudit` at `:1060`. `mockState.audit` is `[]` (`client.ts:146`),
  so a correctly-empty scoped log and an unreachable backend remain byte-identical —
  still not a contract defect, still true on the day the contract predicts everyone
  will see `[]`.
- `smart-calendar-flow/src/api/types.ts:219-224` — the consumer's `AuditEntry` is
  **hand-written**, with `action: string`. The manifest's `schemasChanged.breaking`
  reasons only about the generated union; the audit path does not use generated types at
  all (F4), so the enum has no effect on the frontend today in either direction. Worth
  saying in the manifest, because "the generated TypeScript changes from `string` to a
  seven-member union" reads as a description of the shipped client and is not one.
- `smart-calendar-flow/src/api/audit.test.ts:50, 54` — `action: "focus.run"`, in a
  `fetch` mock, untyped, so it will keep passing. The manifest's handling (this
  feature's frontend stream, one string literal) is correct and the three reasons given
  are good; the honest note that no AC covers it is the right way to carry it.
- `smart-calendar-flow/src/components/QuickActions.tsx:20` — `useAudit(10, showAudit)`,
  an explicit request, not a second default. AC-10 unaffected.
- `backend/api/gen/paceday.gen.go` — **a consumer the manifest does not list.** It is
  generated from this bundle, committed, drift-checked by `make openapi-check`, and it
  already carries this revision's enum and `int32`. See F1, F2, F3.
- `mcp/` — none. Re-ran the search: no match for `audit`, `AuditEntry`, `WriteAuditLog`
  or `/api/audit` anywhere under `mcp/`.
- `e2e/` — none today. Spec AC-5 and AC-13 require a new journey.

### Contract/implementation divergences

**F11 — non-blocking, recorded not re-litigated.** The contract describes the scoped
read, the 50 default and the 500 cap in the present tense while
`backend/api/handlers_audit.go:12-23` and `backend/storage/audit_log.go:15-34` do none
of it — no `WHERE`, `limit <= 0 || limit > 500 → 100`. I re-derived the table in
revision 2 §R1 from the two files and it is exactly right: every out-of-band input
collapses to `100` today, and neither `50` nor `500` is reachable as a resolution. The
previous challenge ruled that factory §7 rule 2 governs defects a feature is *not*
fixing, the author adopted that reading, and I adopt it too — but with one addition the
author's framing misses. Rule 2 is satisfied here not by the reading alone but by the
`CURRENT SERVER BEHAVIOUR` paragraph (`calendar.yaml:760-774`), which states the `100`
with both citations and the finding id *inside the parameter description*. That is the
right construction and the best thing in revision 2: the machine-readable part of the
contract (`default: 50`) is what stage 4 implements, and nothing in the document asserts
something false about the deployed server in the present tense. The API-074 resolution
is therefore real and not papered over **at the contract**; where it is still papered
over is the consumer half of it, which is F4.

### Uncertainties not actually resolved

**F12 — non-blocking. U-02 is genuinely resolved, and I tried again to argue
otherwise.** `grep -n x-uncertain contracts/openapi/paths/calendar.yaml` returns four
markers and I read all four: `:99` (the apparently-dead `GET /api/calendar/freebusy`),
`:234` (the `patchEvent` 200 body), `:901` (`is_personal_block` never set), `:1332`
(the `provider` value set). None is on `/api/audit` and none is on `AuditEntry`, so the
stage-2 gate's rule 3 is satisfied for the one operation this feature touches. The
determination is evidenced, not asserted: `docs/factory/api-audit.md:239` rates the
same behaviour API-005 critical, and the only consumer advertises per-user scope
(`Audit.tsx:38`). I could not construct a reading under which the marker's question —
*whether the global audit log is intentional* — is still open.

The caveats stand and are carried forward rather than raised again: nothing *pins* it
(AC-1/AC-3/AC-4 do not exist, which is acceptable at stage 2 and not at stage 4), and
`docs/factory/api-audit.md:239`, `:308` and `:446` still carry API-005 as `Confirmed`,
API-074 as `Reported` and U-02 as open, so `resolvesFindings` claims a resolution the
register does not yet reflect. The plan schedules that bookkeeping for stage 4
(`PAC-23-plan.md:135`), which is the right place. Flagged for the stage-6 reviewer, as
before.

### Ruling on `contractFragmentHash` versus `contractHash`

I was asked to judge this directly, so: **the diagnosis is right, the evidence is real,
and the remedy overclaims in one sentence that should be fixed before accept.**

What I verified myself, rather than taking from the manifest:

- `sha256sum contracts/openapi/paths/calendar.yaml` →
  `568941e4687ef1e9ebd38a21dc2a0037fa459665368bac9c8d0883be7b4c2bb5`, which **matches**
  the recorded `contractFragmentHash` exactly. PAC-23's own surface has not moved since
  revision 2 was written.
- `sha256sum contracts/openapi/openapi.yaml` →
  `99e4bb41c731db1459b4d31c5b687fdd325ec4b3f8afc57647851b32f1d418ad`, which does **not**
  match the recorded `contractHash` `23099fbc…`.
- `python3 scripts/openapi_assemble.py --check` → `OK: bundle is in sync (98 paths)`.
  So the bundle is not stale relative to the fragments; the hash moved because other
  fragments moved. The author's claim is therefore correct in substance, and correct
  twice over: the recorded value was falsified by edits PAC-23 never made, within the
  session that recorded it.
- The manifest's other recorded figures are all reproducible from the committed bundle
  today: 98 paths, 119 operations, 14 public, 146 schemas, 18 `x-uncertain`.

**So: is a per-fragment hash sufficient to falsify a change to this feature's surface?
No — necessary, much better than the bundle hash, and not sufficient.** It falsifies a
change to the bytes of the fragment PAC-23 owns. The surface of `listAuditEntries` is
not confined to that fragment, and the clearest case is the one that matters most to
this feature:

`/api/audit` carries no `security` key, so it inherits the document-level requirement
(correct, per the convention in `.claude/agents/contract-author.md:39-41`). That
requirement is not in any fragment — it is **synthesised by the assembler**, at
`scripts/openapi_assemble.py:168-172`, and it is conditional:

```python
"security": [{"bearerAuth": []}, {"cookieAuth": []}]
if "cookieAuth" in security_schemes
else [{"bearerAuth": []}],
```

`cookieAuth` is defined in `contracts/openapi/paths/auth.yaml:604` — a fragment PAC-23
does not own, in a domain other features are actively editing. If another feature
renames or removes that scheme, `/api/audit` silently loses cookie authentication from
its security requirement, which is a real change to this operation's surface: cookie is
exactly how the shipped client authenticates it (`credentials: "include"`,
`smart-calendar-flow/src/api/client.ts:349`), and the generated wrapper's
`CookieAuthScopes` for this operation (`paceday.gen.go:6280`) would disappear. The
`calendar.yaml` hash would not move by one bit. The bundle hash would catch it. The same
goes for an edit to the assembler itself, to `backend/oapi-codegen.yaml`, or to any
component this operation `$ref`s that later moves to another fragment.

So keeping both, as revision 2 does, is the right call, and
`.claude/agents/contract-author.md:53` genuinely does require `contractHash` — the
author is right not to have deleted a required field to make a finding go away. What is
wrong is one clause of `contractFragmentHashOf`: *"the fragment hash is the one that
falsifies a change to THIS feature's surface."* It falsifies a change to this feature's
**fragment**. Required before accept, as a manifest text change:

> *"`contractFragmentHash` falsifies a change to the fragment PAC-23 owns. It does not
> cover this operation's effective `security`, which is synthesised by
> `scripts/openapi_assemble.py:168-172` from the `cookieAuth` scheme defined in
> `paths/auth.yaml:604`, nor the two generators' configs. A change to either alters this
> operation's surface without moving the fragment hash; only the bundle hash moves."*

And the structural suggestion is right that this is a factory issue and not PAC-23's to
decide. One shape worth putting in that issue, since it is bookkeeping rather than
implementation: a hash over the **assembled projection of the feature's operations** —
the `paths` subtrees it names, the components those `$ref`, and the effective
`security` — computed from the bundle. That falsifies exactly the feature's surface,
including the assembler-synthesised parts, and is immune to concurrent edits elsewhere.
Neither of the two hashes now in the manifest has both properties.

### Verified clean

Attacked and could not break. The next reader need not repeat these.

- **No `user_id` on `AuditEntry`, no `scope` parameter — second attempt, same result.**
  I tried to construct any request under this contract that addresses another person's
  rows: one operation, one optional integer parameter, no path template, no body,
  `additionalProperties: false` on the response item. There is no vocabulary for it.
  Rejected alternatives A and B are rejected for real reasons and §2's argument — that a
  field on the wire invites a parameter beside it — is still the strongest part of this
  artifact.
- **The seven `action` values, re-derived independently.** `WriteAuditLog`
  (`backend/storage/focus_blocks.go:61`) is the only `INSERT` against the table, and
  `grep -rn WriteAuditLog backend/` returns exactly nine call sites: the seven in the
  enum, with the literals and lines as the contract states them, plus
  `backend/storage/focus_blocks_test.go:105-106` (`test_action`, `another_action`),
  which are a testcontainers fixture no deployed server can emit. The enum is complete
  and the exclusion is correctly reasoned.
- **The `limit` table in revision 2 §R1 is exactly right.** `handlers_audit.go:14`
  discards `Atoi`'s error; `audit_log.go:16-18` is
  `if limit <= 0 || limit > 500 { limit = 100 }`. Absent, `abc`, `0`, `-5` and `10000`
  all become `100`; `50` and `500` pass through. No third number.
- **`details`: four concatenating writers, not three, and the per-writer split is
  correct.** I checked all seven: `focus_time.go:101-106` and
  `focus_time_cleaner.go:33-39` `json.Marshal` a map/struct; `parser.go:221` uses `%q`,
  which escapes; `smart_schedule.go:323`, `handlers_schedule.go:188`,
  `compression.go:178` and `handlers_nlp.go:67` concatenate into a JSON literal with no
  escaping. The correction about `handlers_nlp.go` being a fourth site is right, the
  correction about `compression.go:178` interpolating a client-supplied `event_id`
  rather than a title is right, and keeping `type: string` while making the warning
  per-writer is the right answer to the previous challenge. Rejected alternatives G and
  H are rejected for real reasons, and routing the escaping fix through the **spec**
  keeps the stage ordering intact.
- **`id: int32` is the right declaration.** `audit_log.id` is `SERIAL`
  (`001_initial.up.sql:45`); the Go `int64` (`audit_log.go:9`) is a widening the table
  cannot reach; the generated Go is now `Id int32` (`paceday.gen.go:522`) and the
  generated TS would be `number` either way.
- **The `500`.** `writeError` (`backend/api/handlers_settings.go:206-210`) sets
  `application/json` and encodes `map[string]string{"error": msg}`, so the body is
  exactly `ErrorResponse`, whose `additionalProperties: false` and `required: [error]`
  both hold.
- **`additionalProperties: false` on `AuditEntry`** is safe: the handler encodes
  `[]storage.AuditEntry` (`handlers_audit.go:20-21`) and that struct has exactly the
  four declared fields with exactly those json tags (`storage/audit_log.go:8-13`).
- **The retained NULL-subject rows are unreachable by any other path.** I re-enumerated
  every SQL reference to `audit_log` in `backend/`: one `INSERT`
  (`storage/focus_blocks.go:61-63`), one `SELECT` (`storage/audit_log.go:19`), and the
  `CREATE`/`DROP` in `001_initial`. No view, no join, no second reader, no `UPDATE`.
- **`acceptanceTests` still match the spec's criteria.** I compared all fifteen against
  `docs/specs/PAC-23.md:367-443` one by one. No silent narrowing between revision 1 and
  revision 2, no dropped criterion, and the `[contract]`/`[unit]`/`[e2e]` tags are
  consistent with where each criterion can actually be observed.
- **`rollback` still states the data loss** (`contracts/features/PAC-23.yaml`,
  `rollback`), in the same terms as spec §7 and plan §6, including that a down-then-up
  cycle leaves every user's log permanently empty and that reverting restores the
  API-005 disclosure.
- **The gate passes.** `python3 scripts/openapi_assemble.py --check` and
  `python3 scripts/openapi_migration_report.py --check` both exit 0 — output below.
  `listAuditEntries` is still `handwritten` at `contracts/openapi/MIGRATION.md:75`, and
  the migration report's one-way rule (`openapi_migration_report.py:55-57`) means
  leaving it there does not fail the gate. §6's conclusion is right even though its
  premise is wrong (F3).

### What I could not settle without executing code

Docker is not running, so there is no stack, no database and no test run. Named, with
the criterion that would settle each:

1. **F1/F2 — that the generated binder really returns `400`.** I read the pinned
   generator's runtime source (`oapi-codegen/runtime@v1.1.1`, `bindparam.go:390-412`)
   and the generated wrapper, and the path is unambiguous, but I compiled and ran
   nothing. *Criterion*: a contract test issuing `GET /api/audit?limit=abc`, `?limit=`,
   and `?limit=10&limit=500` against both the hand-written handler and the generated
   wrapper, asserting the status and the declared media type. That test is what decides
   whether this operation has a `400`.
2. **F4 — whether a generated artifact can hand the client the default as a value.**
   Needs `make openapi` in the frontend repo (npm blocked here) to see what
   `openapi-typescript` and `typed-openapi --runtime zod` emit for
   `parameters.limit.schema.default`. *Criterion*: generate both files and grep for
   `50`; if neither carries it as a value, AC-10's second clause needs the
   bundle-comparison test in F4.
3. **Whether `backend/api/gen/paceday.gen.go` is actually in sync with the bundle.** It
   contains this revision's enum and `int32`, so it was regenerated after the
   `calendar.yaml` edits, but `scripts/openapi_gen_go.sh --check` needs `oapi-codegen`
   and the Go toolchain and I did not run it. *Criterion*: `make openapi-check` in a
   local session, pasted.
4. **AC-1, AC-3, AC-4, AC-8, AC-15** — scoping with two users, the pre-migration rows
   and the double-quote round-trip all need a database. Nothing in this contract can
   prove scoping from a single response, which §2 states and accepts as the consequence
   of rejecting alternative A; that trade is correct, and these are the tests that pay
   for it.

### Gate output

Run in this session, exactly as printed:

```
$ python3 scripts/openapi_assemble.py --check
OK: bundle is in sync (98 paths)
EXIT=0
```

```
$ python3 scripts/openapi_migration_report.py --check
OK: MIGRATION.md covers all 119 operations (0 generated)
EXIT=0
```

The assembler was **not** run in write mode: it rewrites
`contracts/openapi/openapi.yaml`, which is a generated file, and a challenger has no
business moving it. The counts quoted in the ruling above were read from the committed
bundle (98 paths, 119 operations, 14 public, 146 schemas, 18 `x-uncertain`), which is
what the manifest records at revision 2.

`make openapi-check`, `make contract`, `make verify`, `scripts/openapi_gen_go.sh
--check`, `go build`, `go test`, `make e2e` and anything npm were **not** run and are
not claimed — Docker is not running in this session. Per factory §8 this is a stage 1–3
environment and those gates belong to a local one.

### Postscript — F13, and the tree moving while I read it

**F13 — non-blocking. The manifest's `rollback` now names a migration number the plan
no longer uses.** `rollback` cites `024_audit_log_user_id.down.sql` twice. While I was
writing this challenge, `docs/factory/PAC-23-plan.md` was renumbered in the working
tree to **025** by somebody else, with the reason stated inline at `:29-35`: PAC-24's
artifacts and PAC-49's spec all call *their* migration `024`, the highest migration on
`main` is `023`, so whichever landed second would have collided. The plan now says
`025_audit_log_user_id.{up,down}.sql` at `:121-122` and `:154`. Nothing is created under
either number yet, so this is a one-word correction, but the manifest is the artifact
stage 7 reads when it rehearses the rollback and it should not name a file that will not
exist. *Fix (manifest text)*: `024` → `025` in `rollback`, or drop the number and say
"the PAC-23 down migration".

For the record, because it bears on every line citation in this challenge: my first
`git status --porcelain` in this session was empty, and by the end `CLAUDE.md`,
`Makefile`, `docs/factory/PAC-23-plan.md` and `scripts/coverage-gate.sh` carried
uncommitted changes I did not make. My own edit is confined to this document — `git
diff --numstat contracts/features/PAC-23.md` reports `615 0`, i.e. insertions only, so
no line of the author's text or of the revision-1 challenge was touched. Citations into
`PAC-23-plan.md` above are against the tree as it stands after that renumbering;
citations into `contracts/openapi/paths/calendar.yaml`, `backend/` and
`smart-calendar-flow/` are against files that did not move. This is the same
concurrent-edit problem revision 2 §U2 is about, observed a third time, and it is a
further argument for the per-feature projection hash suggested above.
