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
