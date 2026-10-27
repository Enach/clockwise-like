# Contract — PAC-34: the invite preview says who invited you and when it expires; manager analytics says when there is no data

- **Linear issue**: [PAC-34](https://linear.app/paceday/issue/PAC-34)
- **Spec**: none (see §0)
- **Stage**: contract-author, then contract-challenger
- **Fragment edited**: `contracts/openapi/paths/teams.yaml` (plus its companion
  `teams.notes.md`, items A2, A7 and A12)
- **Bundle**: regenerated with `python scripts/openapi_assemble.py`; generated Go and
  `MIGRATION.md` by the api gate (`make openapi` in an LF clone). §8 records what was
  run and what it printed.
- **Manifest**: `contracts/features/PAC-34.yaml`

---

## 0. There is no spec, and the challenger should say so

There is no `docs/specs/PAC-34.md`. The issue is a defect bundle from the API audit
(epic D4 in `docs/factory/api-audit.md`). The contract-author definition says to stop
when there is no accepted spec. I went ahead for the same reasons PAC-26, PAC-29,
PAC-30 and PAC-36 did:

- the run that started this stage asked for the contract;
- the run supplied three acceptance criteria that can be falsified;
- each criterion maps to one audit finding whose current behaviour I read in the
  handler (`handlers_teams.go::getInvite`, `handlers_manager.go::getAnalytics`).

This is a deviation from the factory, recorded here rather than hidden. The decisions
in §4 are the ones a spec would have had to settle. With no spec file, they are
recorded in §4 and in the manifest, marked "assumed by the factory, pending owner
confirmation".

### Scope: what the issue body names and what this contract does

| Item | In this contract | Why |
|---|---|---|
| API-021: invite preview lacks `team_id`, `expires_at`, `inviter_email` | yes | Acceptance criterion 1 |
| API-022: manager analytics lacks member `data_available`, `is_paceday_user` | yes | Acceptance criterion 2 |
| API-089: `getInvite` handler comment says "(public)" | yes, comment only | Open finding on an operation this feature touches (factory §7 rule 1). No behaviour change |
| API-086: `team_member_count` on `GET /api/manager/profile` | **no** | Different operation, no acceptance criterion. "Drop" is a breaking removal and "scope" changes the field's meaning; either is an owner call. Follow-up FU-1 |
| `one_on_ones` per analytics member (A7) | **no** | No acceptance criterion. Follow-up FU-3 |

## 1. What changes at the HTTP boundary

| Operation | Before | After | Finding |
|---|---|---|---|
| `getTeamInvite` `GET /api/teams/invites/{token}` | 200 `{teamName, inviterName, email}`. The web reads `team_id`, `expires_at`, `inviter_email` and gets `""`, `undefined`, `undefined`. | 200 also carries **`team_id`** (uuid), **`expires_at`** (RFC 3339 date-time) and **`inviter_email`** (email, or `null` when the inviter row cannot be read). All six keys are required; `additionalProperties: false` kept. | API-021 |
| `getManagerAnalytics` `GET /api/manager/analytics` | Each member is `{email, display_name, weeks}`. The web defaults the missing member `data_available` to `true` and drops the per-week flag, so unmeasured weeks are drawn as zero minutes. | Each member also carries **`is_paceday_user`** and a member-level **`data_available`** (true iff any of its 12 weeks is measured). The per-week `data_available` is unchanged and still required. | API-022 |

Nothing else changes: parameters, status codes, error bodies and the audience of both
operations are as on `main`.

## 2. Why the interface is shaped this way

### 2.1 Required, not optional

Every new key is `required`. The values always exist on the server: `team_id` and
`expires_at` are columns of the invite row the handler has already read,
`inviter_email` is a column of the user row it already reads for `inviterName`, and
`is_paceday_user` / `data_available` are computed from values the analytics loop already
holds. Optional keys would let the web keep its `?? true` default, which is the defect.
A required key makes the generated type say the value is there, so a normalizer that
invents one is visibly wrong.

### 2.2 `inviter_email` is nullable; `teamName`/`inviterName` keep `""`

The two existing keys fall back to `""` when their row cannot be read; I did not change
that (no criterion asks, and their readers handle `""`). For the new key, `""` is not a
valid `format: email`, and "no inviter email" should be distinguishable from "an email".
So the new key is `null` in that case. In practice it is rare: `team_invites.invited_by`
is `NOT NULL REFERENCES users(id) ON DELETE CASCADE` (migration 013), so a deleted inviter
deletes the invite and the preview is a 404. Only a failed read yields `null`.

### 2.3 Member `data_available` is "any bucket measured"

The per-week flag already says, for each bucket, whether it was measured (invariant I7,
PAC-60 gate). The member-level flag answers a different question: is there anything to
draw for this person at all? "Any" answers it without losing data: a member with one
measured week out of 12 still gets a chart, and the 11 unmeasured buckets are drawn as
"no data" by their own flags. It is derived from the buckets in the same response, so it
cannot disagree with them, and it is never cached separately.

### 2.4 `is_paceday_user` matches the roster

`ManagerRosterMember.is_paceday_user` is `member_user_id != nil`. The analytics member
uses the same derivation, so the two endpoints cannot disagree about the same person.

### 2.5 Neither change widens who can see data

- `getManagerAnalytics`: `is_paceday_user` is already returned by `GET /api/manager/team`
  to the same caller for the same members; `data_available` is a function of buckets
  already in this response. The PAC-60 gate is untouched.
- `getTeamInvite`: the audience is unchanged (any authenticated caller holding the
  token, a random UUID sent to the invitee). The response does gain the inviter's email,
  which the issue explicitly asks for. That is more data to the same audience; it is
  recorded as decision D-2 so the owner sees it, and narrowing the audience is FU-2.

## 3. Alternatives rejected

### 3.1 camelCase new keys (`teamId`, `expiresAt`, `inviterEmail`)

The existing preview keys are camelCase, so matching them would make the object
internally consistent, and `normalizeInvite` reads both spellings. Rejected: the
acceptance criteria name `team_id`, `expires_at` and `inviter_email`; every other
manager/teams response the web consumes in snake_case (`team_id` on the roster, scan
preview and confirmation) uses that spelling; and `TeamInvite` (camelCase) is a raw
storage struct the audit already lists as an inconsistency, not a convention to extend.

### 3.2 Rename the three existing keys to snake_case at the same time

This would give one consistent object. Rejected: it is a breaking change to an
operation with a consumer (`normalizeInvite` reads `teamName`), and no criterion asks
for it. Mixed casing is ugly but additive; renaming is a follow-up if the owner wants it.

### 3.3 Echo the full `TeamInvite` (id, token, status, invitedBy, createdAt, expiresAt)

The web type has a `token` field and the creation endpoint already returns this shape.
Rejected: it would expose `invitedBy` (a user id) and the invite id to any token holder,
which is more than any criterion asks for, and `token` is already in the request path.
The preview stays a projection.

### 3.4 Member `data_available` = "every bucket measured"

Rejected: one missing week (a holiday, a new hire's first week) would blank a member who
has eleven measured weeks. The per-bucket flag already marks the missing week.

### 3.5 Drop member `data_available` and make the web derive it from `weeks`

The web could compute `weeks.some(w => w.data_available)` itself. Rejected: the
criterion asks for it on the wire, and the existing normalizer shows what happens when
the client is left to fill a gap: it defaulted to `true`. A required server field leaves
nothing to default.

### 3.6 `data_available` as a three-state enum (`all`, `partial`, `none`)

More expressive for a "partial data" badge. Rejected: no criterion asks for a partial
state, the per-bucket flags already carry it, and a boolean matches the name the web
already reads.

## 4. Decisions (assumed by the factory, pending owner confirmation)

- **D-1**: member `data_available` is true iff any of the 12 buckets is measured (§2.3).
- **D-2**: `inviter_email` is shown to the same audience as `inviterName` today, any
  authenticated holder of the token (§2.5). Narrowing the preview to the invitee is FU-2.
- **D-3**: the new invite keys are snake_case beside the existing camelCase keys;
  nothing is renamed (§3.1, §3.2).
- **D-4**: `inviter_email` is `null`, not `""`, on a failed inviter read (§2.2).
- **D-5**: scope is API-021, API-022 and the API-089 comment; API-086 and `one_on_ones`
  are follow-ups (§0).

None of these deletes user data or widens who can see data beyond what the ticket
explicitly asks for (the inviter's email, D-2).

## 5. Consumers, and what happens to them

Searched both repos for `teams/invites`, `manager/analytics`, `normalizeInvite`,
`normalizeAnalytics`, `TeamInvitePreview` and `ManagerAnalyticsMember`.

| Consumer | Breaks? | What happens |
|---|---|---|
| web `src/api/teams.ts` `normalizeInvite` | no | Already reads `team_id`/`expires_at`/`inviter_email`; they stop being empty. The frontend PR also reads `inviterName`. |
| web `src/api/teamInvites.test.ts` | no | Hand-written fixture keeps passing; AC-2 replaces it with the contract shape. |
| web `src/pages/TeamInvite.tsx` | no | Shows `team_name` only; AC-2 adds inviter and expiry. |
| web `src/api/manager.ts` `normalizeAnalytics` | no | `?? true` default now sees a real value; the frontend PR must stop defaulting and keep the per-week flag (AC-4). |
| web `src/components/team/AnalyticsTab.tsx` `MembersPanel` | no | Draws zeros today; AC-4 makes it show "no data". |
| web `src/api/generated/*` | no | Regenerated after merge, never edited. No hand-written code parses these responses with the generated zod schemas. |
| api `backend/api/gen/paceday.gen.go` | no | Two structs gain fields. Both handlers are hand-written (`MIGRATION.md`). |
| api `backend/api/handlers_manager_test.go` | no | Decodes `weeks`; extra keys do not fail it. |
| api `mcp/`, `e2e/tests` | no | Neither operation is called. |

## 6. Acceptance criteria, as they apply at the HTTP boundary

The three supplied criteria, split by where each can be falsified (full text in the
manifest's `acceptanceTests`):

1. "The invite preview response carries team_id, expires_at and inviter_email" is an
   HTTP-boundary fact: **AC-1** (backend handler test). "the TeamInvite page renders the
   inviter and expiry" is not; it is **AC-2** (web unit) and **AC-5** (e2e).
2. "GET /api/manager/analytics carries data_available and is_paceday_user per member" is
   **AC-3** (backend handler test, including the PAC-60 unowned case). "the web shows
   'no data' instead of zeros" is **AC-4** (web unit) and **AC-5** (e2e).
3. "api-gate, web-gate and e2e-gate rc=0" is **AC-6**, checked at stages 4 and 5.

## 7. `x-uncertain` and audit findings

- Neither operation carries an `x-uncertain` marker (the bundle's 12 remaining markers
  are elsewhere). `resolvesUncertain` is empty.
- Findings on the touched operations: API-021 and API-022 (resolved by this contract
  plus the implementation), API-089 (`getTeamInvite`, comment correction in the
  implementation). API-088 (`team_id` optional on analytics) is an API-shape note the
  audit marks "Confirmed" with no fix proposed; this contract does not change it.

## 8. What was run

- `python scripts/openapi_assemble.py` on the host: assembled 98 paths, 119 operations,
  144 schemas, 12 `x-uncertain`; `--check`: `OK: bundle is in sync (98 paths)`.
- `python scripts/openapi_migration_report.py`: 119 operations, 0 generated.
- `api-gate.sh factory/pac-34-contract` on the first contract commit (`956dbe3`):
  `make openapi rc=0`, regenerated `backend/api/gen/paceday.gen.go` (committed with
  this revision); `openapi-check(api) rc=0` (bundle in sync, MIGRATION.md covers 119
  operations, gen matches); backend lint rc=0, mcp lint rc=0, backend test rc=0, mcp
  test rc=0; backend coverage 56.2% against the 43% floor rc=0; mcp 84.0% against 75%
  rc=0. The web half of openapi-check was SKIPPED by the gate (no web repo in the
  clone); web types are regenerated after this contract merges.
- The gate is re-run on the final commit of this branch; that run is quoted in the
  Linear comment on PAC-34.
- `contractHash` is `git show HEAD:contracts/openapi/openapi.yaml | sha256sum` on this
  branch.
