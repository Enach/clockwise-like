# Contract — PAC-23: the activity log must belong to the person it is about

- **Linear issue**: [PAC-23](https://linear.app/paceday/issue/PAC-23/add-user-id-to-the-audit-log-table-and-scope-get-apiaudit-to-the)
- **Spec**: `docs/specs/PAC-23.md`
- **Manifest**: `contracts/features/PAC-23.yaml` (revision 1, hash
  `2ae421142e7d17eca73c625af3b8f1bb2c397e8bfd92f1fedde4d29377804965`)
- **Stage**: contract-author → contract-challenger
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
