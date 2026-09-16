# booking.yaml — extraction notes

Domain: scheduling links, public booking, conferencing.
24 operations, 23 schemas. Sources: `backend/api/routes.go`,
`handlers_booking.go`, `handlers_scheduling_links.go`, `handlers_conferencing.go`,
`backend/storage/scheduling_links.go`, `backend/conference/*.go`, and the matching
`*_test.go` files. Frontend cross-check: `smart-calendar-flow/src/api/types.ts`,
`schedulingLinks.ts`, `client.ts`, `src/pages/PublicBooking.tsx`.

---

## (a) Backend / frontend mismatches

These are recorded as found. None of them were smoothed over in the YAML — the YAML
documents the **backend**.

### 1. `GET /api/scheduling-links/{id}/bookings` — the frontend reads fields the backend never sends (most serious)

Backend returns raw `storage.Booking` rows:
`{id, link_id, booker_name, booker_email, start_time, end_time, status, notes, created_at}`.

`schedulingLinks.ts` `listBookings` maps them with:

```ts
start: b.start,
end: b.end,
link_slug: b.link_slug ?? "",
title: b.title ?? "Booking",
duration_minutes: b.duration_minutes ?? Math.round((new Date(b.end).getTime() - new Date(b.start).getTime()) / 60_000),
hosts: b.hosts ?? [],
```

`b.start`, `b.end`, `b.link_slug`, `b.title`, `b.duration_minutes` and `b.hosts` do not
exist on the backend payload. Against a real backend every booking row comes out with
`start: undefined`, `end: undefined`, `title: "Booking"`, `link_slug: ""`, `hosts: []`
and `duration_minutes: NaN`. The frontend's `BackendBooking` type declares `start` and
`end` as required — it was written against the *public* confirmation DTO
(`bookingConfirmationDTO`), which does use `start`/`end`, and reused for the owner-facing
bookings list, which does not. Either the backend should map bookings through a DTO, or
the frontend should read `start_time`/`end_time`.

### 2. `createLink` silently drops the user-chosen slug

`schedulingLinksApi.createLink` takes a `slug` argument but `backendLinkBody()` never
emits it. The backend therefore always auto-generates the slug from the owner's name plus
the first duration. The 409 "slug already exists" branch of `POST /api/scheduling-links`
is unreachable from this frontend.

### 3. `co_host_emails` is supported by both create and PATCH but the frontend never uses it

The frontend instead loops `POST /scheduling-links/{id}/hosts` per email
(`Promise.allSettled`, so failures are swallowed). Consequence: the backend's
`co_host_emails` skip-unknown-email behaviour and the PATCH "delete all non-owner hosts
then re-add as pending" behaviour are both untested by the real client. Anyone who does
send `co_host_emails` on PATCH will demote accepted co-hosts back to `pending`.

### 4. `window_start` / `window_end` shape

Frontend `SchedulingLink` types them as `"HH:MM"` and `parseHM` splits on `:` taking the
first two parts. The backend normalises input to `HH:MM` but the DTO value is read back
from a Postgres `TIME` column, so it serialises as `"09:00:00"`
(`handlers_scheduling_links_test.go:91` asserts exactly `"10:00:00"`). `parseHM` survives
this, but an `<input type="time">` bound to the raw value and any string equality check
will not.

### 5. Accept/decline return a body the frontend types as `void`

`acceptInvite`/`declineInvite` are `withFallback<void>`. Backend returns `200` with
`{"status":"accepted"|"declined"}`. Harmless today, but it is not a 204.

### 6. `getPublicSlots` never exercises the no-date mode

The frontend short-circuits and returns an empty result when `date` is absent
("Without a date there is nothing to ask for"). The backend's 60-day `available_dates`
summary — a real, documented response mode — is dead code as far as this client is
concerned. The frontend also tolerates a bare array response (`Array.isArray(raw)`); the
backend never sends one. Same tolerance in `listBookings` for a `{bookings: [...]}`
envelope, which the backend also never sends.

### 7. `bookSlot` sends both `end` and `duration`

The backend cross-checks them and 400s on "duration does not match end time". The
frontend computes `end` from `start + duration_minutes`, so they agree — but this is an
invariant no test enforces on either side. Note the frontend sends `duration`, not
`duration_minutes`; both are accepted, and `duration` wins when both are sent.

### 8. `PublicLinkInfo.coverage` and `min_notice_minutes` typed optional, always sent

Frontend marks them `?`; backend always emits them (`coverage` is a value struct, not a
pointer, so it is `{total:0,checked:0}` at worst). Frontend also declares
`timezone_hint?` and `CoverageSummary.organizer_disconnected?`, neither of which the
backend ever produces for this domain.

### 9. `ConferenceProviderStatus.enabled`

Frontend reads `typeof r.enabled === "boolean" ? r.enabled : undefined`. Backend has
`json:"enabled,omitempty"` on a `bool`, so `false` is **omitted** — the key only ever
appears as `true`, and only for `teams`. Absence is not "unknown", it is "false".

### 10. Frontend calls `/scheduling-links/` with a trailing slash

`requestApi("GET", "/scheduling-links/")` and `POST "/scheduling-links/"`. chi's
`r.Route("/api/scheduling-links", ...)` with `r.Get("/")` matches both spellings, so this
works — noting it only because the spec documents the un-slashed path.

### 11. `POST /api/conference/create` has no frontend consumer

Nothing in `client.ts` or `schedulingLinks.ts` calls it. Its camelCase response
(`joinUrl`, `meetingId`) is unlike every other response in this domain, and no type in
`types.ts` matches it.

---

## (b) Surprises and inconsistencies in the backend

### Duplicate route: `/host-invites` and `/invites`

`routes.go:157-158` registers **the same handler** (`slh.listHostInvites`) at both
`GET /api/scheduling-links/host-invites` and `GET /api/scheduling-links/invites`. Not an
alias with different semantics — literally the same function, same payload. Neither is
marked deprecated in code. The frontend only uses `/host-invites`. Both are documented;
`listSchedulingLinkInvitesAlias` carries the note.

### Duplicate route: `{id}/accept` and `host-invites/{id}/accept`

`routes.go:159-160` and `routes.go:166-167` bind `slh.acceptInvite` / `slh.declineInvite`
to **both** `/host-invites/{id}/accept|decline` and `/{id}/accept|decline`. Since the
handler keys on the link id in both cases (`RespondToHostInvite(db, linkID, userID, ...)`),
the two spellings are exactly equivalent. Four operations in the spec, two behaviours.

### Route ordering — currently safe, but fragile

Within the `/api/scheduling-links` subrouter, `/host-invites` and `/invites` are
registered at lines 157-158, before `/{id}` at line 161. chi's trie matches static
segments before wildcards regardless of registration order, so `GET /invites` can never
be captured by `/{id}` even if the order changed. The same holds for
`/host-invites/{id}/accept` vs `/{id}/accept`: the first segment is static vs wildcard, so
they are distinct nodes and there is no genuine collision. **The real hazard is
semantic, not routing**: `POST /api/scheduling-links/host-invites/{id}/accept` reads as
"accept invite `{id}`", but `{id}` is the **scheduling-link** id, not the invite's `id`.
Anyone who takes the `id` from a `SchedulingLinkHostRecord` (returned by
`POST /{id}/hosts`) and puts it in that path gets a **200 with no effect** — see next
item. The list payload's `link_id` is the correct value.

### Accept/decline are silently no-op on a miss

`RespondToHostInvite` runs a bare `UPDATE ... WHERE link_id=$1 AND user_id=$2` and never
checks `RowsAffected`. Accepting a link you were never invited to, or passing a host-row
id instead of a link id, returns `200 {"status":"accepted"}` having changed nothing.
There is no 404 and no 403 on these endpoints at all. Same pattern in
`leaveSchedulingLink` (`DELETE ... WHERE`, no row check → 204 for a non-host).

### `GET /api/scheduling-links/{id}` has no authorization

Every sibling (PATCH, DELETE, `/bookings`, `/hosts`) enforces `link.OwnerUserID != userID
→ 403`. The GET does not. Any authenticated user who knows a link UUID gets the full
record including every host's email, name and avatar URL. Flagged with `x-uncertain` in
the YAML rather than assumed intentional.

### DELETE is a soft delete, and the two read paths disagree about it

`DeleteSchedulingLink` sets `active = false`. `GetSchedulingLinkBySlug` filters
`AND active = true`, so the public page starts 404ing; `GetSchedulingLinkByID` does not
filter, so `GET /api/scheduling-links/{id}` keeps returning the "deleted" link with
`active: false`. `ListSchedulingLinksByUser` also does not filter on `active`, so
deactivated links stay in the owner's list forever.

### Two 401s with different bodies and content types

- Middleware (`requireAuth`): sets `Content-Type: application/json`, then calls
  `http.Error`, which **overwrites** it with `text/plain; charset=utf-8`. The body is the
  JSON text `{"error":"unauthorized"}\n` served as plain text. The frontend's
  `requestApi` happens to survive this because it only enforces `application/json` on
  2xx.
- `addEventConference` / `removeEventConference` return 401 through `writeError`, i.e. a
  genuine `application/json` `{"error": "<underlying error>"}`. A missing Google token
  produces a 401 that is not an auth-token problem at all.

### Zoom OAuth callback does not use the shared error envelope

`zoomCallback` uses `http.Error` for both its 400 ("invalid state") and its 500s, so those
are `text/plain`, not `{"error": ...}`. `startZoomOAuth`'s 503 *does* use `writeError`.
Two error conventions inside one two-handler flow.

### Alias precedence is inverted between create and PATCH

Create (`schedulingLinkInput.normalized`): `duration_options` wins over `durations`,
`days_of_week` wins over `days`. PATCH (`updateLink`): the aliases are applied in sequence
so `durations` overwrites `duration_options`, and `days` overwrites `days_of_week` — the
opposite precedence. Sending both spellings gives different results depending on the verb.

### `days` alias can silently empty the list

`dayNumbers` drops any token it does not recognise. `{"days":["Montag"]}` becomes `[]`,
which then fails validation as "at least one day is required" — an error message that
does not explain what happened.

### Dead code in `createBooking`

`handlers_booking.go:281-284` re-checks `if err != nil` after the duration validation.
At that point `err` is whatever the last `time.Parse`/`strconv` left behind and cannot be
non-nil on any reachable path. Harmless, but it means the "invalid end time, use RFC3339"
message appears twice in the handler.

### Redundant exhaustion check

`linkExhausted(link)` runs at the top of `createBooking` (→ 410) and then the identical
condition is re-inlined at line 293 (→ 410 again). Two code paths, one status code.

### `uses_count` is computed, not stored

It is a `SELECT COUNT(*) FROM bookings WHERE status <> 'cancelled'` subquery on every
read. Cancelling a booking therefore re-opens a `single_use` link.

### `inviteHost` conflates two 404s

"not found" (link missing) and "user not found" (no account with that email) are both
404 on the same endpoint. A caller cannot distinguish "bad link id" from "invite an
address that has not signed up yet".

### `POST /{id}/hosts` upsert resets accepted hosts

`ON CONFLICT ... DO UPDATE SET status = EXCLUDED.status` with status `'pending'`. Re-inviting
someone who already accepted demotes them, and returns 201 as if it were new.

### Provider list length varies with environment

`GET /api/conference/providers` includes google_meet / zoom / teams only when the
corresponding OAuth env vars are configured, so the array is 1–4 entries. `custom` is
always present and always `connected: true`. A client that indexes `[0]` as google_meet
(as `handlers_conferencing_test.go:117` does) is relying on full configuration.

### `POST /api/conference/create` validates nothing

Missing `title`/`start`/`end` decode to Go zero values and are handed straight to the
provider. A request of `{}` reaches Zoom/Meet with a blank title and year 1.

### `addEventConference` echo branch can return a different provider than requested

If the event already carries a conference and the request asks for `custom`, the handler
returns 200 with the **existing** provider and URL, not a custom one. The `provider` field
of the response is therefore not guaranteed to echo the request.

---

## (c) `x-uncertain` entries in booking.yaml

Three, all narrow:

1. **`GET /api/scheduling-links/{id}`** — missing ownership check. Documented as-is, but
   flagged: it is inconsistent with every sibling route and may be a bug rather than a
   deliberate public-by-UUID design.
2. **`ConferenceMeetingDetails.provider`** — the exact string set written by the concrete
   providers (`meet.go`, `zoom.go`, `teams.go`) was not verified, so no `enum` is asserted.
3. **`SchedulingLink.window_start` / `window_end` format** — documented as `"HH:MM:SS"`
   on the strength of `handlers_scheduling_links_test.go:91`, which pins `"10:00:00"`.
   This depends on the Postgres `TIME` column and the `lib/pq` scan into a Go `string`,
   not on anything in the handler (the handler normalises to `"HH:MM"` before writing).
   Noted inline in the schema description rather than as a separate `x-uncertain` key.

Deliberately **not** marked uncertain, because the code is unambiguous even where the
behaviour is odd: the duplicate routes, the silent no-op accept/decline, the soft delete,
the two 401 shapes, the inverted alias precedence, and the `enabled,omitempty` bool.
