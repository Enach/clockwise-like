# Contract: PAC-81 Part A (backend process hardening)

- **Linear**: PAC-81, https://linear.app/paceday/issue/PAC-81
- **Spec**: `docs/specs/NEW-deploy-hardening.md`, revision 3, challenge
  revision 3 verdict **accept** (branch `factory/deploy-hardening-spec` at
  `beeedc7`, merged into this branch).
- **Stage**: contract-author, revision 1, 2026-10-28. Next: contract-challenger.
- **Manifest**: `contracts/features/PAC-81-part-a.yaml`.
- **Scope**: the five Part A stage-2 items the spec lists in "Revision 3"
  (readiness AC-A11, PAC-14 §2 Sentry default AC-A14, shared budget 503 AC-A2,
  `createPublicBooking` 201 description AC-A19, "treated as free" AC-A18),
  plus two things AC-A2 and AC-A12 imply at the HTTP boundary: the redirect
  rule for sign-in callbacks (spec challenge rev 0 N5) and the backend's JSON
  413. Parts B, C and D have no contract.

Verification: the assembler, migration register and Go generator checks were
run (rc 0, see the manifest's `gates`), and `go build ./...` and `go vet ./...`
pass. No backend behaviour was implemented or observed: every statement about
the server after PAC-81 is what stage 4 must build.

## 1. What changed

| Operation | Change |
|---|---|
| `getReadiness` `GET /api/ready` | **New**, in a new fragment `paths/ops.yaml`. 200 `{"status":"ready"}` / 503 `{"status":"not_ready"}` |
| `createPublicBooking` | 201 description (AC-A19); 503 only before the booking is saved |
| `getPublicBookingSlots` | per-host read rule, own-limit timeout treated as free; 503 |
| `getTeamAvailability` | per-member read rule, own-limit timeout treated as free; 503 |
| `syncPersonalCalendar` | 503 on budget stop (AC-A17); ICS limits named in the 500 |
| `parseNaturalLanguage` | 503; an LLM call's own 30 s timeout is a provider failure (500) |
| API `info.description` | budget rule for every operation, callback redirect exceptions, 413 |
| `PAC-14.md` §2 | amendment: no backend default DSN (AC-A14) |

`getHealth` and `auth.yaml` are untouched. The bundle diff has no hunk in
`/api/health` or `HealthResponse`.

## 2. Why the interface is shaped this way

### Readiness is a separate operation, and liveness does not move

`/api/health` has two consumers that must keep working
(`e2e/global-setup.ts:61-70`; the web's `checkApiAvailability`,
`src/lib/api.ts:28-39`, which switches the web into preview mode on a
non-OK answer). If `/api/health` became readiness, a database outage or the
5 s unready notice of every deploy would put signed-in users into preview
data. So liveness keeps its exact bytes and readiness is a new path.

The path is `/api/ready` so that it sits under nginx's existing `/api/`
proxy rule: the operator's proxy can probe it through nginx (AC-D1) with no
new nginx location. The container check does not use HTTP from outside; it
runs the binary's health mode, which calls readiness on the local port
(AC-A16).

It lives in a new fragment, `ops.yaml`, not next to `getHealth` in
`auth.yaml`. Nothing about readiness is authentication, and PAC-71 (#242)
rewrites `auth.yaml` heavily; a new file cannot conflict with it.

The body is a fixed `status` with no cause. The endpoint is public. Saying
"database unreachable" to anyone who asks tells an attacker when a
database-exhausting attack has worked, and no consumer branches on the
cause: a health check needs only the status code. The cause goes to the log.

### One shared 503 with one fixed message

AC-A2 asks for "a complete JSON error response with status 503". The
contract pins the message `request timed out` as an enum, the same way
`BookingRateLimitError` pins `rate limit exceeded`, so a client can tell a
budget expiry apart from the other 503s that exist today (`Zoom OAuth not
configured`, `Microsoft OAuth not configured`). The schema keeps the
`{"error": ...}` envelope every JSON error uses, so the web's existing
error mapping handles it unchanged (`client.ts:577`: 5xx shows "The server
had a problem. Try again shortly.").

The response component is defined once, in `ops.yaml`, and referenced from
the other fragments. Cross-fragment references are already the norm
(`CalendarNotConnected` is referenced from four fragments and defined in
one). Where an operation has something specific to say about what was kept
(booking create, personal sync, parse), the operation uses its own
description with the shared schema instead of the shared response.

No `Retry-After`. The budget is per request, not a rate. A retry is as likely
to succeed immediately as later, and the header would invite clients to wait
for no reason.

### Booking create: 503 only when nothing was saved

The challenge (rev 3, non-blocking 1 and 2) asked for one sentence the
implementation can be held to: a booking request answers 503 only when no
booking was saved for it. That sentence is now on the 503. It covers the
commit-in-flight edge: if the budget fires while the insert is committing,
the server must find out whether the row exists and answer 201 if it does.
The plan has to show how (manifest `acceptanceTests`, AC-A19).

The 201 description says what is actually guaranteed: the row is saved,
and host events and the email belong to a unit that may finish after the
response. The old text ("host calendar events written") was already false
(the operation's own description says event creation is best effort), so
this replaces a false sentence rather than weakening a true one. No status
was added, as the spec requires (F-12).

### "Treated as free" names the new timeout case

Team availability already documented that a member whose calendar call fails
is treated as free. Slot listing did not, although the code does it
(`backend/engine/booking.go:94-96`, and `hostBusy` ignores a client or list
error). Both now say it. Both also say what changes for users: with the
per-call 15 s limit, a slow calendar now looks free, where before the request
waited. They also draw the line AC-A18 draws. A call that fails on its own
limit counts as free. A call never started, or cut by the budget, is a 503,
never a 200 without that item.

### The budget rule is stated once for the whole API

Through the pool wait (AC-A9), any operation that uses the database can in
principle answer the budget 503. The spec names the five operations that
reach it in normal use, and those carry the 503 explicitly. For the rest,
one paragraph in `info.description` states the rule and its exceptions:
- the sign-in callbacks keep their failure redirect
  (`<FRONTEND_URL>/?error=auth_failed`);
- step-up answers its denied redirect with the reason of the check that was
  running;
- a saved booking answers 201;
- the backend's 413 for bodies over 1 MiB, with the same body as the Part B
  plan's nginx 413.

That paragraph is generated from `scripts/openapi_assemble.py` (`INFO`),
which is where the bundle's info text is defined.

### PAC-14 is amended, not rewritten

PAC-14 §2 made the hard-coded backend DSN the documented default. AC-A14
removes that default. The amendment is a dated block at the top of the "DSN
handling" item, and the original text is left as approved. That keeps the
history readable. The frontend's fallback is out of scope (WEB-sentry-default).

## 3. Alternatives rejected

1. **Make `/api/health` the readiness probe** (with the database check).
   Rejected: it would break the web's preview-mode decision and the meaning
   the e2e global setup relies on, and AC-A11 requires liveness unchanged.
2. **`GET /api/health?ready=1`** (one path, two meanings). Rejected: one
   operation with two contracts. The generated types would merge both
   answers, and a query parameter a proxy drops would turn readiness into
   liveness without anyone noticing.
3. **List the 503 on all 128 operations.** Rejected for this revision: it
   would touch every fragment, including `auth.yaml` under PAC-71's rewrite.
   The extra entries would say nothing beyond the one API-level paragraph,
   for a response that only pool exhaustion can cause on those operations.
   Revisit if a client ever needs the 503 per operation in its generated
   types.
4. **A `reason` enum on the readiness 503** (`database`, `shutting_down`).
   Rejected: see §2. It is public information for no consumer.
5. **A new status (e.g. 202) when the budget expires after a booking is
   saved.** Rejected by the spec (F-12). It would also break the web booking
   page, which treats only 201 as success.
6. **Write the callback redirect rule into each callback's 302 description
   now.** Rejected for now: those four descriptions are being rewritten by
   PAC-71. The rule is in `info.description`, and the manifest records a
   prose-only follow-up to fold it in after PAC-71 merges.

## 4. Merge interplay

- **PAC-71 (#242)**: no `auth.yaml` change here. In `teams.yaml` and
  `scheduling.yaml`, PAC-71 adds a 401 at the top of the responses and this
  contract adds a 503 at the end, so the two should merge cleanly. The
  generated files (bundle, `MIGRATION.md`, `paceday.gen.go`) conflict
  whichever lands second. Regenerate them, never merge them by hand.
- **PAC-72**: rewrites `createPublicBooking`'s description, its 201 and its
  429 status list, and `getPublicBookingSlots`' description. A hand merge is
  unavoidable: keep both texts (manifest `mergeInterplay`). If PAC-72 lands
  first, its pre-insert per-host read is a per-item read under AC-A18.

## 5. Challenge record

None yet.
