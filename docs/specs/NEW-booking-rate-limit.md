# Spec — NEW-booking-rate-limit: Anyone can flood a host's calendar through the public booking page

- **Linear**: none yet. A search of the PAC team for "rate limit" (2026-10-27)
  found only PAC-39 (the `/api/auth/detect` limiter). The factory ticket id is
  `NEW-booking-rate-limit`; a PAC issue should be opened at intake and this file
  renamed or linked from it.
- **Status**: draft
- **Author**: spec-author
- **Inherited scope**:
  - No `x-uncertain` marker sits on the three public booking operations
    (`contracts/openapi/paths/booking.yaml:12-179`; the markers in that file are
    at lines 520 and 1840, on other operations).
  - Open audit findings on the two operations this spec changes:
    API-051 (`docs/factory/api-audit.md:309`, `uses_count` recomputed from live
    bookings) and API-073 (`docs/factory/api-audit.md:331`, dead re-check and
    double `linkExhausted` in the booking handler). Neither is about rate
    limiting. API-051 is in scope of PAC-40 (Backlog, "uses_count recomputed
    from live bookings") and is **not** resolved here. API-073 is dead code in
    the handler this work will touch; the contract stage should decide whether
    "touch it, fix it" (factory README §7) pulls it in — see §8 Q5.
  - API-062 (`docs/factory/api-audit.md`, detect limiter) is the precedent this
    spec reuses for client identification. Its one open residue — limits are
    per process, so N per replica — applies equally to the new limiters and is
    **not** fixed here (§5).
  - `docs/security.md` contradicts itself on this exact boundary: line 46 says
    the anonymous booking surface is "mitigated by per-slug rate limit", while
    line 58 says "Rate limit per slug currently absent — open follow-up" and
    line 89 lists "No rate limit on `/api/book/{slug}` slot fetch + booking
    POST". Line 46 is false today. This work must correct the document (AC-12).

## 1. Problem

A Paceday user shares a public scheduling link so people without an account
can book time with them. Anyone who has the link can submit bookings, and
nothing limits how many. Each accepted booking writes an event into the host's
real calendar (and every co-host's) and sends confirmation mail. A script, or
one bored person, can fill every free slot of a host's next 60 days in a few
seconds, each with a different made-up name and address. The host's calendar
is then useless until they delete the events by hand, and real bookers find no
free time.

The slot listing is also unprotected. Without a date it asks the scheduling
engine about 60 separate days per request, so it is the most expensive public
request the server answers, and anyone can repeat it as fast as they like.

The people affected are hosts (their calendar is the thing flooded), their
co-hosts, and genuine external bookers (who are locked out of the flooded
slots). The cost is a denial of service against one host at a time, with no
account needed and no trace beyond the fake booker emails.

## 2. Current behaviour

- The three public booking routes are registered outside any authentication
  and with no middleware other than the global Sentry, CORS and logging
  wrappers (`backend/api/routes.go:17-19`, `:23-27`).
- Booking creation (`backend/api/handlers_booking.go:210-312`) checks the link
  exists and is not exhausted, validates the body, rejects a start inside the
  minimum-notice window (`:289-292`, 422) and an overlap with an existing
  non-cancelled booking (`:297-303`, 409), then confirms the booking through
  the engine (`:304`), which writes host calendar events. There is no count of
  requests per client anywhere in this path.
- The slot listing (`handlers_booking.go:143-208`) without a `date` loops over
  60 days, calling the engine once per day (`:172-185`); with a `date` it calls
  the engine once (`:195`). No per-client limit.
- The link-info read (`handlers_booking.go:105-122`) is likewise unlimited.
- The only rate limiter in the backend guards `POST /api/auth/detect`
  (`backend/api/handlers_sso.go:24-25`, `backend/api/ratelimit_detect.go`):
  - fixed one-minute window, 20 requests per client (`ratelimit_detect.go:36`,
    `:54-63`);
  - the client is the socket peer, or `X-Real-IP` only when the peer is in
    `TRUSTED_PROXIES` (empty means trust nobody); `X-Forwarded-For` is never
    read (`ratelimit_detect.go:12-18`, `:94-114`);
  - expired buckets are swept at most once per window (`:43-52`), pinned by
    `TestDetectLimiter_ExpiredBucketsAreSwept`
    (`backend/api/ratelimit_detect_test.go:150`);
  - on refusal it answers 429 with `{"error":"rate limit exceeded"}` and **no**
    `Retry-After` header (`handlers_sso.go:25`; `writeError` at
    `handlers_settings.go:388-392` sets only `Content-Type`).
- The contract documents no 429 on any booking operation
  (`contracts/openapi/paths/booking.yaml:24-110`, `:147-179`).
- The web booking page (`smart-calendar-flow/src/pages/PublicBooking.tsx` on
  `origin/main`) handles submit errors 409, 422, 400 and 410 with specific
  messages (`:165-178`) and unreachable-server with a connection message
  (`:179-180`). Any other status, which today would include a 429, falls to
  the generic branch: the server's raw error string is shown and a "Booking
  failed" toast fires (`:181-183`). The slot listing error shows the server's
  error string with a Retry button (`:139-142`, `:419-424`). Queries on this
  page do not auto-retry (`:102`, `:131`, `:160`; app default `src/App.tsx:24`).
- The e2e stack reaches the backend only through nginx
  (`e2e/docker-compose.e2e.yml:12-15`) and does not set `TRUSTED_PROXIES`, so
  under any peer-keyed limiter every e2e request is one client. The suite makes
  roughly 15 booking POSTs per run across `public-booking.spec.ts`,
  `scheduling-links.spec.ts`, `seam.spec.ts` and `time-format.spec.ts`, with
  `fullyParallel: true` (`e2e/playwright.config.ts:32`). This matters for the
  chosen limits (§8 Q1).

## 3. Desired behaviour

A single client can still book normally: open the page, browse a few days,
pick a slot, fail on a taken slot and pick another, and confirm. A client that
submits bookings much faster than any person would is refused for a short
while, before any booking is written and before the link is even looked up.
The refusal says so in machine-readable form: a "too many requests" status, a
JSON error body of the same shape as the endpoints' other errors, and a
standard header telling the client how many seconds to wait.

Browsing slots is limited separately and much more loosely, so a booker who
browses a lot is never prevented from booking, and a booker who has made
several booking attempts can still browse.

"Client" means exactly what it means for the sign-in provider-detection limit
(PAC-39): the network peer, or the proxy-forwarded real address only when the
peer is a configured trusted proxy. A client cannot escape the limit by
sending a forged forwarding header.

The server's memory for these limits stays bounded: clients whose window has
ended are forgotten.

On the booking page, a booker who hits the limit sees a plain, friendly
message telling them they have made too many attempts and to wait a few
minutes, instead of a raw server string and a "Booking failed" alarm. Their
entered name, email, notes and chosen slot are kept, so they can retry
without re-typing.

### Limits (assumed by the factory, pending owner confirmation — see Decisions)

| Surface | Requests allowed per client | Window |
|---|---|---|
| Creating a booking | 20 | 10 minutes, fixed |
| Listing slots / available dates | 120 | 1 minute, fixed |

Every request to the surface counts, whatever its outcome (accepted, invalid,
conflicting, unknown link). Refused requests do not extend the window.
Counting is per client across all links, not per link.

## 4. Acceptance criteria

AC-1. Given a client that has made 20 booking requests within the current
10-minute window, when it makes a 21st booking request to any link, then the
response is 429, the body is a JSON object with an error string, a
`Retry-After` header carries a positive integer number of seconds no greater
than the time remaining in that client's window (rounded up, at least 1), and
no booking exists for that request. [unit]

AC-2. Given the same refused client, when its window has ended, then its next
booking request is processed normally (e.g. 201 for a free slot). [unit]

AC-3. Given a client refused under AC-1, when a different client makes a
booking request, then that request is processed normally. [unit]

AC-4. Given a request whose socket peer is not a trusted proxy, when it
carries an `X-Real-IP` or `X-Forwarded-For` header naming another address,
then it is counted against its socket peer, not the header's address; and
given a peer that **is** a trusted proxy, when it carries a valid `X-Real-IP`,
then it is counted against that address. [unit]

AC-5. Given a client at the booking limit, when it makes a booking request to
a slug that does not exist, then the response is 429, not 404 (the limit is
applied before the link lookup). [unit]

AC-6. Given a client that has made 20 booking requests in the window, when it
lists slots, then it gets 200 (the booking count does not consume the listing
budget); and given a client that has made 120 slot-listing requests in the
current minute, when it makes a booking request, then that request is
processed normally. [unit]

AC-7. Given a client that has made 120 slot-listing requests (with or without
a date) within the current one-minute window, when it makes a 121st, then the
response is 429 with a JSON error body and a `Retry-After` header as in AC-1.
[unit]

AC-8. Given a limiter holding entries for many distinct clients whose windows
have all ended, when the next sweep runs (no later than one window after the
previous sweep), then those entries are gone and only live clients remain.
Proven separately for the booking limiter and the slot-listing limiter. [unit]

AC-9. Given the published contract, when it is read, then both the booking
operation and the slot-listing operation document a 429 response with the
error body schema and a `Retry-After` header, and the limits (count, window,
per-process caveat, client identification) are stated in the operations'
descriptions. The web repository's generated types are regenerated from that
contract in the same delivery. [contract]

AC-10. Given the booking page with a slot chosen and details entered, when the
booking request is answered 429, then the page shows a message saying the
booker has made too many attempts and should wait a few minutes before trying
again; it does not show the server's raw error string; it does not fire the
"Booking failed" error toast; the entered name, email, notes and chosen slot
remain; and the slot list is not refetched automatically. [unit, web]

AC-11. Given the booking page, when the slot listing is answered 429, then the
slots area shows the same kind of friendly wait-and-retry message (not the raw
server string), with the existing manual Retry control. [unit, web]

AC-12. Given `docs/security.md`, when it is read after this work, then it no
longer claims a per-slug limit exists, and the B7 row and the finding list
describe the per-client limits actually in place and the per-process caveat.
[review]

AC-13. Given the e2e stack, when the full e2e suite runs, then it is green
(rc=0): the suite's single-client booking and slot traffic does not trip the
limits as configured on that stack. [e2e]

Why the 429 itself is not an e2e criterion: the e2e stack reaches the backend
through nginx, which overwrites `X-Real-IP`, so every e2e test is the same
client. A test that exhausts the budget would refuse every parallel test's
bookings for the rest of the window. The refusal is proven at unit level on
the backend (AC-1..AC-8) and at component level on the web (AC-10, AC-11);
AC-13 proves the limits do not break real journeys. The challenger may
propose a way to prove AC-1 end to end without poisoning the suite.

## 5. Explicitly out of scope

- **The link-info read** (public link metadata). The ticket names only booking
  and slot listing. It is cheap (one lookup) and does not write. See §8 Q3.
- **Cross-replica limits.** Like the detect limiter (API-062 residue), counts
  are per process: N per replica. A shared store is a separate decision.
- **Adding `Retry-After` to the detect limiter's 429.** Same family, different
  operation; changing it here widens the contract change beyond the ticket.
- **Per-link or per-host limits** (e.g. "no more than K bookings per host per
  hour from anyone"). That would stop a distributed flood, which per-client
  limits cannot, but it lets one attacker lock out every genuine booker of a
  host. Not chosen; see Decisions D-3.
- **CAPTCHA, email verification before confirmation, or nginx-level limits.**
  Different mechanisms; nginx limits are tracked in `docs/security.md:52`/`:85`.
- **API-051 and API-073** (see Inherited scope).
- **Removing bookings already created by a flood.** Nothing is deleted.

## 6. What must not change

- A booking within the limits behaves exactly as today: same statuses (201,
  400, 404, 409, 410, 422, 500), same bodies. Protected by the existing
  booking handler tests and `e2e/tests/public-booking.spec.ts`,
  `scheduling-links.spec.ts`, `seam.spec.ts`, `time-format.spec.ts`.
- Slot listing within the limits: same two modes and same bodies. Protected by
  the same e2e files; backend unit coverage of the no-date mode is thin (the
  plan should name what exists).
- The detect limiter: same 20/min, same keying, same 429 body without
  `Retry-After`. Protected by `backend/api/ratelimit_detect_test.go` and
  `backend/api/pac35_auth_flows_test.go:852`.
- Client identification semantics, including that an unparseable
  `TRUSTED_PROXIES` entry trusts less, never more
  (`ratelimit_detect.go:66-67`). Protected by `ratelimit_detect_test.go`.
- The booking page's existing 409/422/400/410/unreachable messages. Protected
  by `src/pages/PublicBooking.test.tsx` in the web repo.
- No authenticated endpoint gains a limit.

## 7. Rollback

No migration and no stored data: the limits live in process memory. Reverting
the backend change removes the limits and the 429; reverting the contract and
regenerating web types removes the documented 429. The web friendly message is
harmless if left in place after a backend revert (it handles a status the
server then never sends). A restart clears all counters, so an operator can
also relieve a mistakenly-limited client by restarting the backend.

## 8. Open questions

Q1. **Are the limits right, and do they fit the e2e suite?** Owner: product
owner (values); plan-author (e2e fit). 20 bookings per 10 minutes sits above
the ~15 booking POSTs one e2e run makes from one client, but not by much. If
the suite grows, AC-13 will catch it. The plan must either show the suite
stays under the production defaults or let the e2e stack raise them while
production keeps the values in §3; it must not lower them in production to
suit tests. Resolve by owner confirmation of D-1.

Q2. **Office NAT.** Many genuine bookers behind one corporate address share a
budget. 20 per 10 minutes is chosen to make that unlikely to bite, but a
large organisation booking one host's link at once could hit it. Owner:
product owner. Resolve by usage data once deployed.

Q3. **Should the link-info read be limited too?** It enables slug probing
(404 vs 200) at no cost. Slugs are random (`docs/security.md:58`), so this is
low risk. Owner: product owner / security. Resolve in a follow-up ticket if
wanted.

Q4. **Production trusted proxies.** In production the backend sits behind
nginx; unless `TRUSTED_PROXIES` lists it, every booker is one client and the
whole internet shares one budget of 20 bookings per 10 minutes. This is
already true of the detect limiter, but its consequences here are much worse
(genuine bookings refused). Owner: release-manager. Resolve by confirming the
deployed value before release; the release stage must check it.

Q5. **Does touch-it-fix-it pull in API-073?** It is dead code in the booking
handler, low severity, and unrelated to limiting. Owner: contract-challenger
/ plan-author.

## Decisions

All assumed by the factory, pending owner confirmation (2026-10-27). Chosen as
the most conservative options consistent with PRODUCT.md (external bookers are
a named audience; simplicity, no new configuration surface for users) and the
PAC-39 precedent.

- **D-1. Limits.** Booking: 20 requests per client per fixed 10-minute window.
  Slot listing: 120 requests per client per fixed 1-minute window. A person
  books once and retries a handful of times; 20 bounds a single client to 120
  booking attempts an hour instead of thousands a minute. Slot browsing makes
  a request per day clicked, so 120/min is far above any person and still
  caps the 60-day summary's cost. Assumed by the factory, pending owner
  confirmation.
- **D-2. Client identification** is identical to PAC-39's (socket peer;
  `X-Real-IP` only from `TRUSTED_PROXIES`; `X-Forwarded-For` never). Assumed
  by the factory, pending owner confirmation.
- **D-3. Key per client, across all links** — not per (client, link) and not
  per link. Per link would let one attacker lock genuine bookers out of a
  host; per (client, link) would let one client flood many hosts. Assumed by
  the factory, pending owner confirmation.
- **D-4. Every request counts**, including invalid, conflicting and
  unknown-link requests, and the limit is checked before the link lookup.
  Counting only successes would let probing and conflict-spam run unbounded.
  Assumed by the factory, pending owner confirmation.
- **D-5. No user-facing configuration.** Hosts cannot tune the limits; this
  keeps the product's "fewer knobs" position. Assumed by the factory, pending
  owner confirmation.
- **D-6. The refused booker keeps their input** and is told to wait a few
  minutes; the page does not retry automatically. Assumed by the factory,
  pending owner confirmation.

No decision deletes user data or widens who can see data.

## Challenge — 2026-10-27

Reviewed by spec-challenger at commit `d388e32`, committed state only. This
ticket has no Linear issue. A search of team PAC for "booking" on 2026-10-27
found none about rate limiting. This challenge is therefore not posted to
Linear, and §1 is compared against itself because there is no intake text.

**Verdict**: accept-with-changes

### Blocking

1. **No criterion stops a concurrent burst from going over the limit.** Every
   limit criterion (AC-1, AC-5, AC-6, AC-7) is sequential: "has made 20
   requests ... when it makes a 21st". The attack in §1 is a script that
   floods a host "in a few seconds", and a script sends its requests in
   parallel. A limiter that reads the count, does the work and only then
   increments passes AC-1..AC-8 as written. Yet it still admits a parallel
   burst of any size from one client in a fresh window. The spec therefore
   permits the very pattern it exists to refuse. **Resolve by** adding a
   criterion of this form: Given a client with no requests in the current
   window, when it sends K > 20 booking requests concurrently, then at most 20
   are processed past the limit, and every other one is answered 429 with no
   booking written. Add the same for the slot-listing limit at 120.

### Non-blocking

1. **A booking is never checked against the host's availability, so §1
   understates the flood.** `createBooking` checks only two things: the
   minimum notice (`backend/api/handlers_booking.go:289-292`) and overlap with
   this link's own bookings (`:297-303`, via `storage.HasOverlappingBooking`
   at `backend/storage/scheduling_links.go:388-394`). `ConfirmBooking` has a
   "Re-verify availability" comment (`backend/engine/booking.go:222`) but
   then only fetches the hosts and inserts the row. A flood is therefore not
   confined to "every free slot of the next 60 days". Any start after the
   notice window is accepted, including times outside the link's window and
   during the host's busy time. This spec is right not to fix that, since it
   is not about limiting. It is, however, an unrecorded defect in the handler
   this work touches. It should be entered in `docs/factory/api-audit.md` and
   ticketed. §1 and §2 should also say it exists, so nobody reads the limits
   as confining the damage to free slots.
2. **Flood mail reaches third parties the attacker chooses, not only the
   booker.** Each host's calendar event lists the booker's email as an
   attendee (`backend/engine/booking.go:256-260`), and that address also gets
   the confirmation mail (`:275-276`). §1 names hosts, co-hosts and genuine
   bookers as affected. It leaves out the arbitrary recipients who are sent
   invitations from a Paceday host's calendar. This makes the case for the
   limit stronger, and §1 should name them.
3. **AC-12 rewrites the B7 row but leaves two other false claims in place.**
   `docs/security.md:46` claims a "booker-email confirmation flow", but
   bookings are confirmed immediately (`handlers_booking.go:304-311`).
   `docs/security.md:58` claims "Server-side validation of booking time vs
   availability", which item 1 shows does not exist. If AC-12's review only
   removes "per-slug", the row it edits is still contradicted by the code.
   Consider widening AC-12 to: "the B7 row and line 46 make no claim the code
   contradicts".
4. **AC-8's "no later than one window after the previous sweep" reads like a
   timer.** The precedent sweeps only when a request arrives
   (`backend/api/ratelimit_detect.go:43-52`), so with no traffic no sweep
   runs. The criterion can still be tested, because it is conditioned on
   "when the next sweep runs". Writing "on the first request after one window
   has passed since the last sweep" would remove the ambiguity for the plan.
5. **Q4 leaves out what the repo already ships.** `docker-compose.prod.yml:47`
   defaults `TRUSTED_PROXIES` to `172.16.0.0/12,192.168.0.0/16,10.0.0.0/8`, so
   the production compose already trusts the bundled nginx. The real question
   for the release stage is whether nginx sees the true client address. Nginx
   sets `X-Real-IP $remote_addr` (`nginx/nginx.conf:8`), which covers only one
   hop. The bundled config also has no TLS server, although the prod compose
   publishes port 443. If anything terminates TLS or proxies in front of
   nginx, every booker becomes one client whatever `TRUSTED_PROXIES` says. Q4
   should be restated in those terms.
6. **The spec does not say how IPv6 clients are grouped.** A "client" is a
   full address. If the edge preserves IPv6 source addresses, one person can
   rotate addresses within their /64 and is in effect unlimited. If it does
   not, all IPv6 bookers may share one budget: the bundled nginx listens on
   `80` only, so IPv6 traffic may arrive translated to a single address. I
   did not verify how the deployed host behaves. §5 lists cross-replica and
   distributed floods as accepted gaps. This one belongs there too, unless §3
   states the grouping instead.
7. **The e2e margin is tighter than §2 says in one case.** I count about 10
   booking POSTs per run, not 15: `public-booking.spec.ts` lines 137, 171,
   192, 202 and 310 plus the UI journeys, `scheduling-links.spec.ts:88`,
   `seam.spec.ts:287` and `time-format.spec.ts:114`. However, with
   `E2E_KEEP_STACK=1` (`e2e/global-teardown.ts`) the backend process survives
   into the next run. Two runs inside 10 minutes then make about 20 POSTs
   from one client. Q1 already hands the e2e fit to the plan, and it should
   cover the kept-stack case.
8. **A page on another origin cannot read `Retry-After`.** The header is not
   on the CORS safe list, and the CORS middleware sets no
   `Access-Control-Expose-Headers` (`backend/api/middleware.go:61-85`).
   Nothing in this spec needs the web to read it, since AC-10 only says "a
   few minutes", so nothing breaks. If the web is ever meant to show the real
   wait, the spec would need to require that the header be exposed.
9. OWNER: is slowing one client from seconds to hours the outcome you want?
   At 20 per 10 minutes, one IPv4 client can still write 120 bookings an hour
   (D-1). That fills a typical host's bookable 60 days in an afternoon, and a
   handful of addresses can do it in minutes. §5 deliberately rejects a
   per-host cap (D-3) because one attacker could then lock out every genuine
   booker. §3 states the limit accurately. §1, however, frames the problem as
   "the host's calendar is useless", and the chosen limits make that slower,
   not impossible.
10. OWNER: please confirm three decisions. D-1 sets the limit values. D-3
    counts per client across all links rather than per host. D-4 counts
    failed and unknown-link requests, so a genuine booker who gets a few 409s
    uses up part of their budget.
11. OWNER: Q3, whether the link-info read should also be limited.

### Criteria I could not falsify

- None. Each of AC-1..AC-11 describes a test that fails today. No booking
  route answers 429 yet. For any status it does not list, the web page shows
  the raw server string and fires the "Booking failed" toast
  (`smart-calendar-flow/src/pages/PublicBooking.tsx:181-183` on
  `origin/main`). AC-12 can be checked today by reading
  `docs/security.md:46`. AC-13 is a regression guard that turns red only if
  the limits are sized wrongly, which is its stated purpose.

### Citations I checked and found wrong

- None was wrong. I checked:
  - `routes.go:17-19, :23-27`.
  - `handlers_booking.go:105-122, :143-208, :172-185, :195, :210-312, :289-292,
    :297-303, :304`.
  - `ratelimit_detect.go:12-18, :36, :43-52, :54-63, :66-67, :94-114`.
  - `handlers_sso.go:24-25` and `handlers_settings.go:388-392`.
  - `ratelimit_detect_test.go:150` and `pac35_auth_flows_test.go:852`.
  - `api-audit.md:309, :331` and API-062.
  - The `x-uncertain` markers in `paths/booking.yaml`, at lines 520 and 1840.
    Neither is on a public booking operation.
  - That no 429 is documented under the three public operations.
  - `docs/security.md:46, :52, :58, :85, :89`.
  - `e2e/docker-compose.e2e.yml:12-15`, which sets no `TRUSTED_PROXIES`, and
    `e2e/playwright.config.ts:32`.
  - The web citations `PublicBooking.tsx:102, :131, :139-142, :160, :165-183`
    and `App.tsx:24`, on `origin/main`.

  The only imprecision is the "roughly 15" count of e2e POSTs (non-blocking
  item 7).

### Consumers this breaks

- `smart-calendar-flow/src/pages/PublicBooking.tsx:181-183`: a 429 would show
  "rate limit exceeded" and fire the "Booking failed" toast. AC-10 and AC-11
  cover this.
- `clockwise-like/e2e/tests/*`, which all reach the backend as one client
  behind nginx: covered by AC-13, with the kept-stack caveat in non-blocking
  item 7.
- Neither repo has any other consumer of the three public routes. The MCP
  server does not call them, and the only web caller is `PublicBooking.tsx`,
  through `src/api/schedulingLinks.ts`.
- The backend tests that call the booking handlers
  (`backend/api/handlers_booking_test.go`, 12 references) stay under the
  booking limit. They do, however, share one `httptest` peer address for the
  whole package run. The plan must reset the limiter between tests, as
  `resetDetectLimiter` does (`backend/api/ratelimit_detect_test.go:18-30`).
